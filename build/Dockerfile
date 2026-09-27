# Builds the service from source into the same runtime shape as
# release.Dockerfile, which expects a binary GoReleaser already built. Used by
# the compose smoke stand: it tests the binary an instance would run, without
# air, the debugger or a mounted source tree.
FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X main.version=smoke" -o /eye ./cmd/eye

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /eye /eye
EXPOSE 8080
CMD ["/eye"]
