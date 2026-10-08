package store

import "ant/server/internal/domain"

type InventoryRecord struct {
	Namespace      domain.ID
	Version        int64
	DefinitionJSON []byte
	UpdatedAt      domain.Millis
}
type InventoryReaderTx interface {
	GetInventory(namespace domain.ID) (InventoryRecord, bool, error)
}
type InventoryWriterTx interface {
	InventoryReaderTx
	SaveInventoryCAS(expectedVersion int64, record InventoryRecord) (bool, error)
}
