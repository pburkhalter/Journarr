import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// History is the Flow page with the "Done" filter. The old URL (bookmarks,
// links) keeps working and carries its search along.
export const load: PageLoad = ({ url }) => {
	const params = new URLSearchParams({ status: 'done' });
	const q = url.searchParams.get('q');
	if (q) params.set('q', q);
	redirect(307, `/?${params}`);
};
