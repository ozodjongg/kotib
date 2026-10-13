# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags gotd -o /out/secretary ./cmd/secretary

# distroless: shell yo'q, root emas — hujum yuzasi minimal.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/secretary /app/secretary
COPY webapp /app/webapp
ENV STATIC_DIR=/app/webapp \
    LISTEN=0.0.0.0:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/secretary"]
