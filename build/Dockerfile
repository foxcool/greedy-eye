FROM golang:1.23-alpine AS builder

ENV PROJECT_PATH=github.com/Foxcool/greedy-eye

# get CMD path argument
ARG _path

# Set environment variable for Go
ENV GOPATH=/go \
	PATH="/go/bin:$PATH"

# Copy project files
WORKDIR ${GOPATH}/src/${PROJECT_PATH}
COPY go.mod .
COPY go.sum .
COPY cmd cmd
COPY pkg pkg

# Build
WORKDIR ${_path}
RUN CGO_ENABLED=0 GOOS=linux go build -a -o ${GOPATH}/bin/app .

# Init new lightweight container
FROM alpine:3.20
RUN apk --no-cache add ca-certificates


COPY --from=builder /go/bin/app .

# Run the compiled bin by default when the container start.
CMD /app
