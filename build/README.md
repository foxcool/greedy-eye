# Build docker image

## Login to Docker Hub

```bash
docker login
```

## Create a builder

```bash
docker buildx create --name mybuilder --use
docker buildx inspect --bootstrap
```

## Build the image and push it to Docker Hub

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -f build/Dockerfile \
  --build-arg _path=cmd/eye \
  -t foxcool/greedy-eye:latest \
  --push .
```
