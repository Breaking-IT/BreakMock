FROM golang:1.25 AS build
RUN apt-get update && apt-get install -y --no-install-recommends zlib1g-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -tags netgo,osusergo -trimpath -ldflags="-s -w -linkmode external -extldflags '-static'" -o /breakmock ./cmd/breakmock && mkdir -p /data
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /breakmock /breakmock
COPY --from=build --chown=65532:65532 /data /data
USER 65532:65532
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/breakmock"]
