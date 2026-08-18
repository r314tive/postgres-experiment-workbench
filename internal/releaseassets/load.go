package releaseassets

import "github.com/r314tive/postgres-experiment-workbench/internal/strictjson"

const maxInventoryBytes = int64(2 << 20)

// LoadFile reads a bounded regular non-symlink file and strictly decodes one
// inventory. Duplicate and unknown properties, explicit null values, and
// trailing JSON are rejected by the shared strict JSON loader.
func LoadFile(path string) (Inventory, error) {
	var inventory Inventory
	if err := strictjson.LoadFile(path, maxInventoryBytes, &inventory); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}

// Parse strictly decodes one in-memory inventory.
func Parse(content []byte) (Inventory, error) {
	var inventory Inventory
	if err := strictjson.Parse(content, &inventory); err != nil {
		return Inventory{}, err
	}
	return inventory, nil
}
