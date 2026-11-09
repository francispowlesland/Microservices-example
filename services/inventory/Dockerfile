FROM golang:1.27 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY gen/ gen/
COPY internal/ internal/
COPY services/inventory/ services/inventory/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/inventory ./services/inventory/cmd/inventory

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/inventory /inventory
ENV APP_ENV=production \
    GRPC_ADDR=:50054
EXPOSE 50054
HEALTHCHECK --interval=10s --timeout=5s --start-period=40s --retries=3 CMD ["/inventory", "healthcheck"]
ENTRYPOINT ["/inventory"]
