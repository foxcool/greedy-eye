package user

import grpc "google.golang.org/grpc"

type UserService struct {
	UnimplementedUserServiceServer
}

func NewUserService() *UserService {
	return &UserService{}
}

func (s *UserService) Register(server *grpc.Server) {
	RegisterUserServiceServer(server, s)
}
