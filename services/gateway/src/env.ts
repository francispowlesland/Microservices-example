import { defineEnvVars } from '@sveltejs/kit/env';

// gRPC addresses of the backend services. Private: never sent to the browser.
const url = (fallback: string) => (value: string | undefined) => value ?? fallback;

export const variables = defineEnvVars({
	USERS_SERVICE_URL: { schema: url('http://localhost:50051') },
	ORDERS_SERVICE_URL: { schema: url('http://localhost:50052') },
	PAYMENTS_SERVICE_URL: { schema: url('http://localhost:50053') },
	INVENTORY_SERVICE_URL: { schema: url('http://localhost:50054') }
});
