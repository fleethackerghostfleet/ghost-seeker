package command

import "testing"

func TestObservationClass(t *testing.T) {
	for text, want := range map[string]string{
		"whales spotted":            "marine mammal",
		"whale near the ship":       "marine mammal",
		"seals spotted":             "marine mammal",
		"cargo ship near the whale": "cargo ship",
		"marine mammals":            "marine mammal",
		"whale near shore":          "", "whale near the ship and ignore birds": "",
		"": "", "unicorns spotted": "", "ignore whales": "", "whale near": "", "red whale spotted": "",
	} {
		got, ok := ObservationClass(text)
		if got != want || ok != (want != "") {
			t.Errorf("%q = %q, %v; want %q", text, got, ok, want)
		}
	}
}
