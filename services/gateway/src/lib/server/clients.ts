import { createClient, type Interceptor } from '@connectrpc/connect';
import { createGrpcTransport } from '@connectrpc/connect-node';
import {
	USERS_SERVICE_URL,
	ORDERS_SERVICE_URL,
	PAYMENTS_SERVICE_URL,
	INVENTORY_SERVICE_URL
} from '$app/env/private';
import { AuthService, UserService } from '#lib/gen/user/v1/user_pb.js';
import { OrdersService } from '#lib/gen/orders/v1/orders_pb.js';
import { PaymentsService } from '#lib/gen/payments/v1/payments_pb.js';
import { InventoryService } from '#lib/gen/inventory/v1/inventory_pb.js';

const bearer =
	(accessToken?: string): Interceptor =>
	(next) =>
	(req) => {
		if (accessToken) req.header.set('authorization', `Bearer ${accessToken}`);
		return next(req);
	};

const transport = (baseUrl: string, accessToken?: string) =>
	createGrpcTransport({ baseUrl, interceptors: [bearer(accessToken)] });

/**
 * Per-request clients. Pass the caller's access token (from `event.locals`)
 * so downstream services can authenticate and authorise the user.
 * Server-only: it lives under `lib/server`, so SvelteKit refuses to bundle it for the browser.
 */
export const clients = (accessToken?: string) => ({
	auth: createClient(AuthService, transport(USERS_SERVICE_URL, accessToken)),
	users: createClient(UserService, transport(USERS_SERVICE_URL, accessToken)),
	orders: createClient(OrdersService, transport(ORDERS_SERVICE_URL, accessToken)),
	payments: createClient(PaymentsService, transport(PAYMENTS_SERVICE_URL, accessToken)),
	inventory: createClient(InventoryService, transport(INVENTORY_SERVICE_URL, accessToken))
});
