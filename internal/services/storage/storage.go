package storage

import (
	"log/slog"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
)

const (
	// DefaultPageSize is the default page size for pagination.
	DefaultPageSize = 100
)

type StorageService struct {
	services.UnimplementedStorageServiceServer

	log      *slog.Logger
	dbClient *ent.Client
}

func NewService(dbClient *ent.Client, logger *slog.Logger) *StorageService {
	service := &StorageService{
		dbClient: dbClient,
		log:      logger,
	}

	return service
}
