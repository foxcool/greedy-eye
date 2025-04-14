package user

import (
	"github.com/foxcool/greedy-eye/internal/api/services"
)

type UserService struct {
	services.UnimplementedUserServiceServer
}

func NewService() *UserService {
	return &UserService{}
}
