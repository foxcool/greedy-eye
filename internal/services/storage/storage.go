package storage

import (
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"go.uber.org/zap"
)

type StorageService struct {
	services.UnimplementedStorageServiceServer

	log      *zap.Logger
	dbClient *ent.Client
}

func NewService(dbClient *ent.Client, logger *zap.Logger) *StorageService {
	service := &StorageService{
		dbClient: dbClient,
		log:      logger,
	}

	return service
