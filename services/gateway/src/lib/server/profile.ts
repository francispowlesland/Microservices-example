import { fail } from '@sveltejs/kit';
import { api } from './clients';
import { failFrom, text } from './rpc';

/** UpdateUser with a mask of just the fields that changed. Shared by /account and /users/[id]. */
export async function updateProfile(locals: App.Locals, id: string, data: FormData) {
	const name = text(data, 'name');
	const username = text(data, 'username').toLowerCase();
	const paths = [
		name !== text(data, 'was_name') && 'name',
		username !== text(data, 'was_username') && 'username'
	].filter((p): p is string => !!p);
	if (paths.length === 0) return { on: 'profile', saved: true };
	if (!name || !username)
		return fail(400, { on: 'profile', message: 'Name and username can’t be empty.' });
	try {
		await api(locals).users.updateUser({ user: { id, name, username }, updateMask: { paths } });
		return { on: 'profile', saved: true };
	} catch (e) {
		return failFrom(e, 'profile');
	}
}
