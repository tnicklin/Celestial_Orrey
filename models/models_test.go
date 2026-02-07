package models

import "testing"

func TestKeyIDOrSyntheticPrefersKeyID(t *testing.T) {
	key := CompletedKey{
		KeyID:     123,
		Character: "Arthas",
		Region:    "us",
		Realm:     "illidan",
	}
	got := key.KeyIDOrSynthetic()
	if got != "key:123" {
		t.Fatalf("expected key:123, got %s", got)
	}
}

func TestSyntheticKeyStableAndSensitive(t *testing.T) {
	key := CompletedKey{
		Character:   "Arthas",
		Region:      "us",
		Realm:       "illidan",
		Dungeon:     "Mists of Tirna Scithe",
		KeyLevel:    10,
		RunTimeMS:   1320000,
		ParTimeMS:   1500000,
		CompletedAt: "2026-02-01T01:23:45Z",
		Source:      "raiderio",
	}
	a := key.SyntheticKey()
	b := key.SyntheticKey()
	if a != b {
		t.Fatalf("expected stable synthetic key, got %s and %s", a, b)
	}

	key.KeyLevel = 11
	c := key.SyntheticKey()
	if a == c {
		t.Fatalf("expected synthetic key to change when fields change")
	}
}
