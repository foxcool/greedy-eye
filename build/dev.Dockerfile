FROM golang:alpine
ENV PROJECT_PATH=github.com/foxcool/greedy-eye

# Get CMD path argument (default: cmd/eye)
ARG _path="cmd/eye"

# Set environment variable for Go
ENV GOPATH=/go \
    PATH="/go/bin:$PATH"

# Copy project files
WORKDIR ${GOPATH}/src/${PROJECT_PATH}
COPY go.mod go.sum ./

# Install air
RUN go install github.com/air-verse/air@latest
