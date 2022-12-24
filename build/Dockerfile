FROM golang:1.19-alpine AS builder

# Install dependencies
RUN apk add --no-cache \
	git

ENV PROJECT_PATH=github.com/Foxcool/greedy-eye

# get CMD path argument
ARG _path

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
FROM alpine:3.11
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /go/bin/instance .

# Run the compiled bin by default when the container start.
CMD /app/instance
