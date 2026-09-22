package giftpack

import "testing"

func TestParseManifestRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"empty pack name", `{"gifts":[{"title":"A","stars":1,"base_animation":"a.json"}]}`},
		{"no gifts", `{"pack_name":"P","gifts":[]}`},
		{"missing title", `{"pack_name":"P","gifts":[{"stars":1,"base_animation":"a.json"}]}`},
		{"zero stars", `{"pack_name":"P","gifts":[{"title":"A","base_animation":"a.json"}]}`},
		{"convert exceeds stars", `{"pack_name":"P","gifts":[{"title":"A","stars":5,"convert_stars":10,"base_animation":"a.json"}]}`},
		{"missing base animation", `{"pack_name":"P","gifts":[{"title":"A","stars":1}]}`},
		{"duplicate title", `{"pack_name":"P","gifts":[{"title":"A","stars":1,"base_animation":"a.json"},{"title":"A","stars":1,"base_animation":"b.json"}]}`},
		{"upgrade missing slug", `{"pack_name":"P","gifts":[{"title":"A","stars":1,"base_animation":"a.json","upgrade":{"models":[{"name":"M","animation":"m.json"}],"patterns":[{"name":"P","animation":"p.json"}],"backdrops":[{"name":"B"}]}}]}`},
		{"upgrade missing models", `{"pack_name":"P","gifts":[{"title":"A","stars":1,"base_animation":"a.json","upgrade":{"slug_prefix":"a","patterns":[{"name":"P","animation":"p.json"}],"backdrops":[{"name":"B"}]}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.json)); err == nil {
				t.Fatalf("ParseManifest(%s) = nil error, want a validation error", tc.name)
			}
		})
	}
}

func TestParseManifestAcceptsWellFormedManifest(t *testing.T) {
	data := []byte(`{
		"pack_name": "Test Pack",
		"author": "tester",
		"gifts": [
			{"title": "Basic", "stars": 15, "base_animation": "basic.json"},
			{
				"title": "Upgradeable",
				"stars": 25,
				"convert_stars": 25,
				"base_animation": "upgradeable.json",
				"upgrade": {
					"upgrade_stars": 50,
					"supply_total": 100,
					"slug_prefix": "up",
					"models": [{"name": "M1", "animation": "m1.json", "permille": 500}, {"name": "M2", "animation": "m2.json", "permille": 500}],
					"patterns": [{"name": "P1", "animation": "p1.json", "permille": 500}, {"name": "P2", "animation": "p2.json", "permille": 500}],
					"backdrops": [{"name": "B1", "center": "#ff0000", "edge": "#00ff00", "pattern": "#0000ff", "text": "#ffffff", "permille": 500}, {"name": "B2", "center": "#111111", "edge": "#222222", "pattern": "#333333", "text": "#444444", "permille": 500}]
				}
			}
		]
	}`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.PackName != "Test Pack" || len(m.Gifts) != 2 {
		t.Fatalf("parsed manifest = %+v, want pack_name=Test Pack and 2 gifts", m)
	}
	if m.Gifts[1].Upgrade == nil || len(m.Gifts[1].Upgrade.Models) != 2 {
		t.Fatalf("gifts[1].upgrade = %+v, want 2 models", m.Gifts[1].Upgrade)
	}
}
