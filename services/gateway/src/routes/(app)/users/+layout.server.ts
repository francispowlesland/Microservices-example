import { error } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';

// The users service enforces this too; this just gives non-admins a clear page instead of a 403 mid-render.
export const load: LayoutServerLoad = ({ locals }) => {
	if (!locals.session?.isAdmin) error(403, 'Only admins can manage users.');
};
