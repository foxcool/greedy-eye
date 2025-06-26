FROM golang:alpine AS builder

ENV PROJECT_PATH=github.com/foxcool/greedy-eye

# get CMD path argument (default: cmd/eye)
ARG _path="cmd/eye"

# Set environment variable for Go
ENV GOPATH=/go \
    PATH="/go/bin:$PATH"

# Copy project files
WORKDIR ${GOPATH}/src/${PROJECT_PATH}

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build from project root, specifying the cmd path
RUN CGO_ENABLED=0 GOOS=linux go build -o ${GOPATH}/bin/app ./${_path}

# Init new lightweight container
FROM alpine
RUN apk --no-cache add ca-certificates curl
RUN curl -sSf https://atlasgo.sh | sh

COPY deploy/migrations /migrations
COPY --from=builder /go/bin/app .

# Run the compiled bin by default when the container start.
CMD [ "/app" ]
