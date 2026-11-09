# Documentation

| Page | Read it when |
|---|---|
| [architecture.md](architecture.md) | You want the shape of the system: services, databases, who talks to whom, shared packages. |
| [auth.md](auth.md) | You're touching tokens, keys, roles or the gateway's sessions. |
| [local-development.md](local-development.md) | You want it running, or tested, on your machine. |
| [protobuf-and-codegen.md](protobuf-and-codegen.md) | You're changing a `.proto` file or regenerating code. |

Per-service details (environment variables, endpoints, quirks) are in each service's README:
[users](../services/users/README.md), [orders](../services/orders/README.md), [payments](../services/payments/README.md), [inventory](../services/inventory/README.md), [gateway](../services/gateway/README.md).
