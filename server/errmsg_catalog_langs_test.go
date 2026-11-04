package server

import (
	"testing"

	"github.com/Autumn-27/artex/config"
)

// Every catalog entry must carry a translation for each supported non-Korean
// language, so a newly added message cannot ship untranslated in some locale.
func TestErrCatalogCoversAllSupportedLanguages(t *testing.T) {
	for key, tr := range errCatalog {
		for _, lang := range config.SupportedLanguages {
			if lang == "ko" { // the map key is the Korean source text
				continue
			}
			if tr[lang] == "" {
				t.Errorf("errCatalog entry %q has no %q translation", key, lang)
			}
		}
	}
}
