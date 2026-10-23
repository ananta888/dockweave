# dockweaved daemon image (runs privileged on the Docker host).
# docker run --rm --privileged --pid=host \
#   -v /var/lib/dockweave:/var/lib/dockweave \
#   -v /run/dockweave:/run/dockweave \
#   -v /var/run/docker.sock:/var/run/docker.sock dockweave/dockweaved
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/dockweaved ./cmd/dockweaved \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/dockweave ./cmd/dockweave \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/dockweave-aimonitor ./cmd/dockweave-aimonitor

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/dockweaved"]
