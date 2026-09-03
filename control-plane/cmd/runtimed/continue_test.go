package main

import "testing"

func TestAnnouncesNextStep(t *testing.T) {
	stops := []string{
		"Now the Offer cards:",
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
