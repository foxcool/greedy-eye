FROM alpine:latest
RUN apk --no-cache add ca-certificates curl
RUN curl -sSf https://atlasgo.sh | sh
COPY schema.hcl /schema.hcl
COPY atlas.hcl /atlas.hcl
COPY eye /eye
EXPOSE 8080
CMD ["/eye"]
