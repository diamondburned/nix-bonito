package bonito

import "encoding/json"

type nixRegistry map[string]flakesRegistryV2Flake

type flakesRegistryV2 struct {
	Flakes []flakesRegistryV2Flake
}

func (f flakesRegistryV2) convertToNixRegistry() nixRegistry {
	registry := make(nixRegistry)
	for _, flake := range f.Flakes {
		from, ok := flake.From.(flakesRegistryV2FromIndirect)
		if !ok {
			continue
		}
		registry["bonito:"+from.ID] = flake
	}
	return registry
}

func (f flakesRegistryV2) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Flakes  []flakesRegistryV2Flake `json:"flakes"`
		Version int                     `json:"version"`
	}{
		Flakes:  f.Flakes,
		Version: 2,
	})
}

type flakesRegistryV2Flake struct {
	From flakesRegistryV2From `json:"from"`
	To   flakesRegistryV2To   `json:"to"`
}

type flakesRegistryV2From interface {
	json.Marshaler
	flakesFrom()
}

type flakesRegistryV2FromIndirect struct {
	ID string `json:"id"`
}

func (f flakesRegistryV2FromIndirect) flakesFrom() {}

func (f flakesRegistryV2FromIndirect) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}{
		Type: "indirect",
		ID:   f.ID,
	})
}

type flakesRegistryV2To interface {
	json.Marshaler
	flakesTo()
}

type flakesRegistryV2ToPath struct {
	Path string `json:"path"` // [path:]<path>(\?<params)?
}

func (f flakesRegistryV2ToPath) flakesTo() {}

func (f flakesRegistryV2ToPath) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}{
		Type: "path",
		Path: f.Path,
	})
}
