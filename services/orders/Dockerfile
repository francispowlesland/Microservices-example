FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY gen/ gen/
COPY internal/ internal/
COPY services/orders/ services/orders/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/orders ./services/orders/cmd/orders

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/orders /orders
ENV GRPC_ADDR=:50052 \
    APP_ENV=production
EXPOSE 50052
HEALTHCHECK --interval=10s --timeout=5s --start-period=40s --retries=3 CMD ["/orders", "healthcheck"]
ENTRYPOINT ["/orders"]
