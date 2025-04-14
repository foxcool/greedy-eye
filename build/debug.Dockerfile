FROM golang:alpine AS builder

# Install delve debugger
RUN go install github.com/go-delve/delve/cmd/dlv@latest

#ENV GO111MODULE=off
ENV PROJECT_PATH=github.com/Foxcool/greedy-eye

# get CMD path argument (default: cmd/eye)
ARG _path="cmd/eye"

# Set environment variable for Go
ENV GOPATH=/go \
	PATH="/go/bin:$PATH"

# Copy project files
WORKDIR ${GOPATH}/src/${PROJECT_PATH}
COPY . .


# Build
WORKDIR ${_path}
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags "-X main.version=develop" -gcflags "all=-N -l" -o ${GOPATH}/bin/instance .

# Init new lightweight container
FROM alpine:3.20
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /go/bin/instance .
COPY --from=builder /go/bin/dlv .


# Run the debugger with compiled bin by default when the container start.
CMD ["/app/dlv", "--listen=:40000", "--headless=true", "--api-version=2", "exec", "/app/instance"]

# Service listens on hardcoded port.
EXPOSE 80 40000
