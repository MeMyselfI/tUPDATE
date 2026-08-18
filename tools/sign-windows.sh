#!/usr/bin/env bash
#
# sign-windows.sh — Authenticode-sign Windows .exe files with a Certum
# Open Source Code Signing certificate held in the SimplySign cloud HSM.
#
# The private key never leaves Certum's cloud; it is reached through the
# SimplySign PKCS#11 module (SimplySign Desktop must be running AND logged
# in via the mobile-app token/OTP, otherwise the token is invisible).
#
# The signing certificate's PKCS#11 object ID is looked up dynamically at
# runtime (via pkcs11-tool), so a yearly certificate renewal — which mints a
# new key/ID — needs no edit here.
#
# The public certificates live in a shared directory outside any repo
# (~/.certum), so the yearly renewal is done once for every project that signs.
# The repo-local signing/ directory still works as a fallback.
#
# Usage:  tools/sign-windows.sh <file.exe> [<file2.exe> ...]
#
# Overridable via environment:
#   SIGN_DIR     shared cert directory        default: ~/.certum, else signing/
#   SIGN_ENGINE  PKCS#11 engine (libp11)      default: Homebrew engines-3/pkcs11.dylib
#   SIGN_MODULE  SimplySign PKCS#11 module     default: /usr/local/lib/libSimplySignPKCS.dylib
#   SIGN_CHAIN   cert chain (leaf+intermediate) default: $SIGN_DIR/chain.pem
#   SIGN_AC      intermediate(s) for -ac        default: $SIGN_DIR/inter.pem
#   SIGN_TS      RFC3161 timestamp URL          default: http://time.certum.pl
#   SIGN_HASH    digest algorithm               default: sha256
#   SIGN_FORCE   re-sign even if already signed default: (unset)
#
set -euo pipefail

# Certs come from the shared ~/.certum by default; older checkouts that still
# carry their own signing/ keep working.
if [ -z "${SIGN_DIR:-}" ]; then
  if [ -d "$HOME/.certum" ]; then SIGN_DIR="$HOME/.certum"; else SIGN_DIR="signing"; fi
fi

ENGINE="${SIGN_ENGINE:-/opt/homebrew/lib/engines-3/pkcs11.dylib}"
MODULE="${SIGN_MODULE:-/usr/local/lib/libSimplySignPKCS.dylib}"
CHAIN="${SIGN_CHAIN:-$SIGN_DIR/chain.pem}"
# osslsigncode takes the signing certificate from -pkcs11cert, which makes it
# ignore -certs entirely — so the intermediate must be handed over separately
# via -ac, or only the leaf ends up in the signature block and Windows cannot
# build a chain to the trusted root ("unknown publisher" in the UAC prompt).
AC="${SIGN_AC:-$SIGN_DIR/inter.pem}"
TS="${SIGN_TS:-http://time.certum.pl}"
HASH="${SIGN_HASH:-sha256}"

die() { echo "sign-windows: $*" >&2; exit 1; }

[ "$#" -ge 1 ] || die "no input files given (usage: sign-windows.sh <file.exe> ...)"

# --- preflight: tools + files -------------------------------------------------
command -v osslsigncode >/dev/null 2>&1 || die "osslsigncode not found (brew install osslsigncode)"
command -v pkcs11-tool  >/dev/null 2>&1 || die "pkcs11-tool not found (brew install opensc)"
[ -f "$ENGINE" ] || die "PKCS#11 engine not found: $ENGINE (brew install libp11)"
[ -f "$MODULE" ] || die "SimplySign module not found: $MODULE (install SimplySign Desktop)"
[ -f "$CHAIN"  ] || die "cert chain not found: $CHAIN (chain.pem = leaf + intermediate; see ~/.certum)"
[ -f "$AC"     ] || die "intermediate cert not found: $AC (fetch repository.certum.pl/ccsca2021.cer)"

# --- preflight: token present + dynamic key ID --------------------------------
# pkcs11-tool prints object attributes; the private key's ID looks like:
#   ID:         c1:ae:ee:...:4e
objects="$(pkcs11-tool --module "$MODULE" -O 2>/dev/null || true)"
printf '%s\n' "$objects" | grep -q 'Private Key Object' \
  || die "SimplySign token not visible — start SimplySign Desktop and log in (token-ID + OTP)."

keyid_colons="$(printf '%s\n' "$objects" \
  | awk '/Private Key Object/{f=1} f&&/ID:/{print $2; exit}')"
[ -n "$keyid_colons" ] || die "could not read private-key ID from token."

# c1:ae:ee -> %c1%ae%ee  (RFC7512 pk11 URI id encoding)
keyid_uri="$(printf '%s' "$keyid_colons" | sed 's/^/%/; s/:/%/g')"

KEYURI="pkcs11:id=${keyid_uri};type=private"
CERTURI="pkcs11:id=${keyid_uri};type=cert"

echo "sign-windows: token OK, key id ${keyid_colons}"

# --- sign each file -----------------------------------------------------------
for exe in "$@"; do
  [ -f "$exe" ] || { echo "sign-windows: skip (missing): $exe" >&2; continue; }

  # osslsigncode verify exits non-zero when the root isn't locally trusted
  # (normal on macOS), so capture output and grep it — never rely on the
  # pipeline's exit status under `set -o pipefail`.
  if [ -z "${SIGN_FORCE:-}" ]; then
    vout="$(osslsigncode verify "$exe" 2>/dev/null || true)"
    if printf '%s' "$vout" | grep -q 'Signature Index'; then
      echo "sign-windows: already signed, skip: $exe  (set SIGN_FORCE=1 to override)"
      continue
    fi
  fi

  tmp="${exe}.signing.$$"
  osslsigncode sign \
    -engine "$ENGINE" \
    -pkcs11module "$MODULE" \
    -pkcs11cert "$CERTURI" \
    -key "$KEYURI" \
    -certs "$CHAIN" \
    -ac "$AC" \
    -h "$HASH" \
    -ts "$TS" \
    -in "$exe" \
    -out "$tmp"
  mv -f "$tmp" "$exe"

  # confirm a signature is now embedded (trust-to-root is a Windows-side check)
  vout="$(osslsigncode verify "$exe" 2>/dev/null || true)"
  printf '%s' "$vout" | grep -q 'Signature Index' \
    || die "verification found no signature after signing: $exe"

  # confirm the signature block carries leaf AND intermediate — with only the
  # leaf, Windows has to fetch the intermediate over AIA and the UAC prompt
  # falls back to "unknown publisher" whenever that fetch is slow or blocked.
  sigtmp="${exe}.sig.$$"
  osslsigncode extract-signature -in "$exe" -out "$sigtmp" >/dev/null 2>&1 \
    || die "could not extract signature for chain check: $exe"
  ncerts="$(openssl pkcs7 -inform DER -in "$sigtmp" -print_certs -noout 2>/dev/null \
    | grep -c '^subject=' || true)"
  rm -f "$sigtmp"
  [ "$ncerts" -ge 2 ] \
    || die "signature block holds only $ncerts certificate(s) — intermediate missing (check -ac $AC): $exe"

  echo "sign-windows: signed  $exe  (${ncerts} certs in signature block)"
done
