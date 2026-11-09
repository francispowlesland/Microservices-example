FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY gen/ gen/
COPY internal/ internal/
COPY services/payments/ services/payments/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/payments ./services/payments/cmd/payments

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/payments /payments
# Non-secret defaults. DATABASE_URL and JWT_PUBLIC_KEY are supplied at runtime only.
ENV GRPC_ADDR=:50053 \
    APP_ENV=production \
    PAYMENT_MAX_CENTS=1000000
EXPOSE 50053
HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=3 CMD ["/payments", "healthcheck"]
ENTRYPOINT ["/payments"]
