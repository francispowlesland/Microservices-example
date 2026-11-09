import type { LayoutServerLoad } from './$types';

// Only what the UI needs. The access token stays in its httpOnly cookie and never reaches the browser.
export const load: LayoutServerLoad = ({ locals }) => ({
	viewer: locals.session ? { id: locals.session.userId, isAdmin: locals.session.isAdmin } : null
});
