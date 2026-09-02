FROM alpine:latest
RUN apk --no-cache add ca-certificates curl
# Pinned, because the upgrade path now depends on what `atlas migrate apply`
# does: an unpinned installer would let a future release change the meaning of
# a deploy without a single line of this repository changing.
RUN ATLAS_VERSION=v1.3.2 sh -c 'curl -sSf https://atlasgo.sh | sh'
COPY schema.hcl /schema.hcl
COPY atlas.hcl /atlas.hcl
# The ordered migrations travel with the binary they belong to, so an instance
# upgrades from the image alone: `atlas migrate apply --dir file:///migrations`.
# A tag therefore carries both what the schema should be and how to get there
# from the previous one.
COPY migrations /migrations
COPY eye /eye
EXPOSE 8080
CMD ["/eye"]
