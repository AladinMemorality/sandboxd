package main

import "testing"

func TestAnnouncesNextStep(t *testing.T) {
	stops := []string{
		"Now the Offer cards:",
		"Still double-encoded even with a jsonb cast on a plain string. The client itself appears to serialize the parameter again and the data is still incorrect. Actually let me test the default database client directly, outside this application wrapper:",
		"The database is connected and the build passed, but meeting storage is still failing because the application is writing JSON strings instead of arrays. I will inspect the parameter types in a separate probe before trying another fix.",
		"Now I'll update the manifest categories with a script (exact filenames from disk):",
		"Hero is wired.\n\nLet me do the results section next.",
		"Next, the testimonials gallery",
		"**Now wiring the hero**",
	}
	for _, s := range stops {
		if !announcesNextStep(s) {
			t.Errorf("expected a stop: %q", s)
		}
	}
	ends := []string{
		"Done. The hero, the three bundle cards and the results section now use the uploaded files; the manifest categories match.",
		"Wired every section to public/media. Verified with a screenshot: no placeholders remain.",
		"",
		"Now the page renders all 28 uploads, the videos play inline, and the admin categories match what is shown; nothing was left out because every file the request named was on disk and every section that needed one has it.",
	}
	for _, s := range ends {
		if announcesNextStep(s) {
			t.Errorf("expected an end: %q", s)
		}
	}
}
