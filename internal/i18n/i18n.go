// Package i18n provides locale-aware UI strings for the updater.
// Locale detection is environment-based and works the same on Linux, macOS,
// and (when configured) Windows. Supported languages: English (default),
// German, French. Anything else falls back to English.
package i18n

import (
	"os"
	"strings"
)

// Lang identifies one of the supported UI languages.
type Lang int

const (
	LangEN Lang = iota
	LangDE
	LangFR
)

// String returns the ISO 639-1 two-letter code of the language.
func (l Lang) String() string {
	switch l {
	case LangDE:
		return "de"
	case LangFR:
		return "fr"
	default:
		return "en"
	}
}

// Strings holds every user-facing message used by the updater for one language.
// Entries with %s or %d use fmt-style verbs and must be formatted by the caller.
type Strings struct {
	// CLI lifecycle
	BetaWarning   string // Banner shown on every invocation.
	LogfileLabel  string // "Logfile:"
	StartedMarker string // "=== updater %s started: %s ==="
	EndedMarker   string // "=== updater ended: exit=%d, %s ==="

	// Errors / status
	LogfileError           string
	ConfigError            string
	NoServiceCommandConfig string // "%s" → GOOS
	UsingLocalZip          string // "Using local ZIP:"
	ZipNotFound            string
	HTTPClientError        string
	TempFileError          string
	TempDirError           string
	DownloadStart          string // "Downloading:"
	DownloadFailed         string
	DownloadHint           string // "Hint: use --zip <path> for a local file."
	ExtractError           string
	PathError              string
	PromptError            string

	// Service
	ServiceStopping   string // "Stopping service:"
	ServiceStopError  string
	ServiceStarting   string // "Starting service:"
	ServiceStartError string

	// Diff
	ComputingDiff   string
	DiffError       string
	DryRunDone      string
	NoChanges       string
	WrapperDetected string // "Wrapper folder in ZIP detected: %s — using it as reference root."

	// Backup / Apply
	BackupQuestion               string
	BackupCreating               string
	BackupLabel                  string
	BackupError                  string
	UpdateQuestion               string
	UpdateAborted                string
	ContinueOrShowQuestion       string // "Continue, abort, or show full file list?"
	ContinueAfterDetailsQuestion string // "Continue with these changes?"
	SuffixContinueOrShow         string // "[Y/n/a]" / "[J/n/a]" / "[O/n/a]"
	DetailsHeader                string // header above file list
	LegendAdded                  string // "[+] added"
	LegendModified               string // "[~] modified"
	LegendRemoved                string // "[-] removed"
	ApplyingUpdate               string
	SyncError                    string
	RestoreFromBackup            string
	UpdateSuccess                string
	Done                         string
	ContinueAnyway               string

	// Prompt UI
	SuffixYesDefault string // shown when default=true, e.g. "[Y/n]"
	SuffixNoDefault  string // shown when default=false, e.g. "[y/N]"
	RetryMessage     string // shown on invalid input
}

var en = Strings{
	BetaWarning:   "!!! WARNING: This tool is in BETA. Use with caution and make sure you have a backup before running it. !!!",
	LogfileLabel:  "Logfile:",
	StartedMarker: "=== updater %s started: %s ===",
	EndedMarker:   "=== updater ended: exit=%d, %s ===",

	LogfileError:           "Logfile error:",
	ConfigError:            "Config error:",
	NoServiceCommandConfig: "No service commands configured for GOOS=%s.",
	UsingLocalZip:          "Using local ZIP:",
	ZipNotFound:            "ZIP not found:",
	HTTPClientError:        "HTTP client error:",
	TempFileError:          "Temp file error:",
	TempDirError:           "Temp dir error:",
	DownloadStart:          "Downloading:",
	DownloadFailed:         "Download failed:",
	DownloadHint:           "Hint: use --zip <path> for a local file.",
	ExtractError:           "Extract error:",
	PathError:              "Path error:",
	PromptError:            "Prompt error:",

	ServiceStopping:   "Stopping service:",
	ServiceStopError:  "Service stop error:",
	ServiceStarting:   "Starting service:",
	ServiceStartError: "Service start error:",

	ComputingDiff:   "Computing diff...",
	DiffError:       "Diff error:",
	DryRunDone:      "Dry run finished, no changes made.",
	NoChanges:       "No changes.",
	WrapperDetected: "Wrapper folder in ZIP detected: %s — using it as reference root.",

	BackupQuestion:               "Create backup of current directories?",
	BackupCreating:               "Creating backup...",
	BackupLabel:                  "Backup:",
	BackupError:                  "Backup error:",
	UpdateQuestion:               "Apply update now?",
	UpdateAborted:                "Update aborted by user.",
	ContinueOrShowQuestion:       "Continue, abort, or show the full file list?",
	ContinueAfterDetailsQuestion: "Continue with these changes?",
	SuffixContinueOrShow:         "[Y/n/a]",
	DetailsHeader:                "Detailed changes:",
	LegendAdded:                  "[+] added",
	LegendModified:               "[~] overwritten",
	LegendRemoved:                "[-] removed",
	ApplyingUpdate:               "Applying update...",
	SyncError:                    "Sync error:",
	RestoreFromBackup:            "Backup available for restore:",
	UpdateSuccess:                "Update successful.",
	Done:                         "Done.",
	ContinueAnyway:               "Continue anyway?",

	SuffixYesDefault: "[Y/n]",
	SuffixNoDefault:  "[y/N]",
	RetryMessage:     "Please enter 'y' or 'n'.",
}

var de = Strings{
	BetaWarning:   "!!! WARNUNG: Dieses Tool befindet sich in der BETA-Phase. Vorsichtig verwenden und vorher unbedingt sichern. !!!",
	LogfileLabel:  "Logfile:",
	StartedMarker: "=== updater %s gestartet: %s ===",
	EndedMarker:   "=== updater beendet: exit=%d, %s ===",

	LogfileError:           "Logfile-Fehler:",
	ConfigError:            "Config-Fehler:",
	NoServiceCommandConfig: "Keine Service-Kommandos für GOOS=%s konfiguriert.",
	UsingLocalZip:          "Verwende lokale ZIP:",
	ZipNotFound:            "ZIP nicht gefunden:",
	HTTPClientError:        "HTTP-Client-Fehler:",
	TempFileError:          "Temp-Datei-Fehler:",
	TempDirError:           "Temp-Dir-Fehler:",
	DownloadStart:          "Download:",
	DownloadFailed:         "Download fehlgeschlagen:",
	DownloadHint:           "Tipp: --zip <Pfad> für lokale Datei nutzen.",
	ExtractError:           "Extract-Fehler:",
	PathError:              "Pfad-Fehler:",
	PromptError:            "Prompt-Fehler:",

	ServiceStopping:   "Service stoppen:",
	ServiceStopError:  "Service-Stop-Fehler:",
	ServiceStarting:   "Service starten:",
	ServiceStartError: "Service-Start-Fehler:",

	ComputingDiff:   "Diff berechnen...",
	DiffError:       "Diff-Fehler:",
	DryRunDone:      "Dry-Run beendet, keine Änderungen.",
	NoChanges:       "Keine Änderungen.",
	WrapperDetected: "Wrapper-Ordner im ZIP erkannt: %s — wird als Referenz-Root verwendet.",

	BackupQuestion:               "Backup der aktuellen Verzeichnisse erstellen?",
	BackupCreating:               "Backup wird erstellt...",
	BackupLabel:                  "Backup:",
	BackupError:                  "Backup-Fehler:",
	UpdateQuestion:               "Update jetzt durchführen?",
	UpdateAborted:                "Update vom Benutzer abgebrochen.",
	ContinueOrShowQuestion:       "Weitermachen, abbrechen oder die vollständige Dateiliste anzeigen?",
	ContinueAfterDetailsQuestion: "Mit diesen Änderungen fortfahren?",
	SuffixContinueOrShow:         "[J/n/a]",
	DetailsHeader:                "Detaillierte Änderungen:",
	LegendAdded:                  "[+] neu",
	LegendModified:               "[~] überschrieben",
	LegendRemoved:                "[-] gelöscht",
	ApplyingUpdate:               "Update wird angewendet...",
	SyncError:                    "Sync-Fehler:",
	RestoreFromBackup:            "Backup zum Wiederherstellen:",
	UpdateSuccess:                "Update erfolgreich.",
	Done:                         "Fertig.",
	ContinueAnyway:               "Trotzdem fortfahren?",

	SuffixYesDefault: "[J/n]",
	SuffixNoDefault:  "[j/N]",
	RetryMessage:     "Bitte 'j' oder 'n' eingeben.",
}

var fr = Strings{
	BetaWarning:   "!!! AVERTISSEMENT : Cet outil est en BÊTA. Utilisez-le avec précaution et effectuez une sauvegarde au préalable. !!!",
	LogfileLabel:  "Fichier de log :",
	StartedMarker: "=== updater %s démarré : %s ===",
	EndedMarker:   "=== updater terminé : exit=%d, %s ===",

	LogfileError:           "Erreur de fichier de log :",
	ConfigError:            "Erreur de configuration :",
	NoServiceCommandConfig: "Aucune commande de service configurée pour GOOS=%s.",
	UsingLocalZip:          "Utilisation du ZIP local :",
	ZipNotFound:            "ZIP introuvable :",
	HTTPClientError:        "Erreur client HTTP :",
	TempFileError:          "Erreur de fichier temporaire :",
	TempDirError:           "Erreur de répertoire temporaire :",
	DownloadStart:          "Téléchargement :",
	DownloadFailed:         "Échec du téléchargement :",
	DownloadHint:           "Astuce : utilisez --zip <chemin> pour un fichier local.",
	ExtractError:           "Erreur d'extraction :",
	PathError:              "Erreur de chemin :",
	PromptError:            "Erreur de saisie :",

	ServiceStopping:   "Arrêt du service :",
	ServiceStopError:  "Erreur d'arrêt du service :",
	ServiceStarting:   "Démarrage du service :",
	ServiceStartError: "Erreur de démarrage du service :",

	ComputingDiff:   "Calcul du diff...",
	DiffError:       "Erreur de diff :",
	DryRunDone:      "Dry-run terminé, aucune modification.",
	NoChanges:       "Aucune modification.",
	WrapperDetected: "Dossier wrapper détecté dans le ZIP : %s — utilisé comme racine de référence.",

	BackupQuestion:               "Créer une sauvegarde des répertoires actuels ?",
	BackupCreating:               "Création de la sauvegarde...",
	BackupLabel:                  "Sauvegarde :",
	BackupError:                  "Erreur de sauvegarde :",
	UpdateQuestion:               "Effectuer la mise à jour maintenant ?",
	UpdateAborted:                "Mise à jour annulée par l'utilisateur.",
	ContinueOrShowQuestion:       "Continuer, annuler ou afficher la liste complète des fichiers ?",
	ContinueAfterDetailsQuestion: "Poursuivre avec ces modifications ?",
	SuffixContinueOrShow:         "[O/n/a]",
	DetailsHeader:                "Modifications détaillées :",
	LegendAdded:                  "[+] ajouté",
	LegendModified:               "[~] écrasé",
	LegendRemoved:                "[-] supprimé",
	ApplyingUpdate:               "Application de la mise à jour...",
	SyncError:                    "Erreur de synchronisation :",
	RestoreFromBackup:            "Sauvegarde disponible pour restauration :",
	UpdateSuccess:                "Mise à jour réussie.",
	Done:                         "Terminé.",
	ContinueAnyway:               "Continuer malgré tout ?",

	SuffixYesDefault: "[O/n]",
	SuffixNoDefault:  "[o/N]",
	RetryMessage:     "Veuillez saisir 'o' ou 'n'.",
}

// Get returns the Strings bundle for the requested language.
// Unknown values fall back to English.
func Get(l Lang) Strings {
	switch l {
	case LangDE:
		return de
	case LangFR:
		return fr
	default:
		return en
	}
}

// Detect inspects locale environment variables and returns the matching Lang.
// Inspection order: LC_ALL, LC_MESSAGES, LANG. The first non-empty value's
// two-letter ISO prefix decides the language. "C" / "POSIX" / unsupported
// languages fall back to LangEN.
func Detect() Lang {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		val := strings.TrimSpace(os.Getenv(key))
		if val == "" {
			continue
		}
		if len(val) < 2 {
			continue
		}
		prefix := strings.ToLower(val[:2])
		switch prefix {
		case "de":
			return LangDE
		case "fr":
			return LangFR
		case "en":
			return LangEN
		}
	}
	return LangEN
}
