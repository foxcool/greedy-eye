package storage

import (
	"github.com/foxcool/greedy-eye/internal/api/services"
)

type StorageService struct {
	services.UnimplementedStorageServiceServer
}

func NewService() *StorageService {
	return &StorageService{}
}
