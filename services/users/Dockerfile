FROM golang:1.27 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY gen/ gen/
COPY internal/ internal/
COPY services/users/ services/users/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/users ./services/users/cmd/users

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/users /users
ENV APP_ENV=production \
    GRPC_ADDR=:50051
EXPOSE 50051
HEALTHCHECK --interval=10s --timeout=5s --start-period=40s --retries=3 CMD ["/users", "healthcheck"]
ENTRYPOINT ["/users"]
