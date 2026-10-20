// Types mirror the Go JSON. Only the fields the client reads are listed.

export type Size = 'S' | 'M' | 'L';
export type Energy = 'low' | 'med' | 'high';
export type Status = 'open' | 'done' | 'dropped';
export type Tier = 'must' | 'should' | 'could';
export type Source = 'user' | 'chat' | 'cleanup' | 'triage';
export type Provider = 'anthropic' | 'openai' | 'claudecode' | 'copilot';
/** Where a provider's access comes from. Empty means there is none; login means its CLI was found. */
export type KeySource = '' | 'keychain' | 'env' | 'login';

/** Whether a provider runs on the user's own sign-in to its vendor's CLI, and takes no key. */
export function signsIn(p: Provider): boolean {
	return p === 'claudecode' || p === 'copilot';
}

/** A link or key pulled out of a task's text. */
export interface Ref {
	label: string;
	url?: string;
}

export interface Task {
	id: number;
	title: string;
	notes: string;
	raw_text: string;
	size: Size | null;
	energy: Energy | null;
	feeling: string;
	due_on: string | null;
	/** A push sets this. The task is put off until then. */
	not_before: string | null;
	status: Status;
	refs: Ref[] | null;
	created_at: string;
	updated_at: string;
}

/** What a task write sends. Every field is sent; null clears one. */
export interface TaskFields {
	title: string;
	notes: string;
	size: Size | null;
	energy: Energy | null;
	feeling: string;
	due_on: string | null;
	not_before: string | null;
	refs: Ref[];
}

/** TaskID can't start until BlockedByID is finished. */
export interface Blocker {
	task_id: number;
	blocked_by_id: number;
}

export interface CalendarEvent {
	id: number;
	title: string;
	starts_at: string;
	ends_at: string | null;
	created_at: string;
}

export interface Day {
	date: string;
	pace: number;
	started_at: string;
	wrapped_at: string | null;
	tomorrow_note: string;
	summary: string;
	reflection: string;
}

export type Action = 'roll_over' | 'push' | 'drop';

/** The model's advice for one unfinished task. Until is set only for a push. */
export interface Suggestion {
	task_id: number;
	reason: string;
	action: Action;
	until: string;
}

/** A day's outcome, for wrap-up and the morning's reconcile. Fallback means code suggested. */
export interface Review {
	day: Day;
	done: Task[];
	unfinished: Task[];
	suggestions: Suggestion[];
	fallback: boolean;
}

/** What the user decided for one unfinished task. A push with no until goes to tomorrow. */
export interface Triage {
	task_id: number;
	action: Action;
	until: string | null;
}

/** The five pace levels, 1 to 5, as the server labels them. */
export const paceLabels = ['running on fumes', 'gentle', 'steady', 'full steam', 'go hard'];

export interface PlanEntry {
	task_id: number;
	tier: Tier;
	rationale: string;
}

export interface Plan {
	id: number;
	opening: string;
	entries: PlanEntry[];
	fallback: boolean;
}

export interface Today {
	date: string;
	day: Day | null;
	plan: Plan | null;
	unplanned: number[];
}

export interface Pick {
	task_id: number;
	reason: string;
}

export interface ToolUse {
	tool: string;
	label: string;
	error: string;
}

export interface Message {
	id: number;
	role: 'user' | 'assistant';
	text: string;
	tools: ToolUse[] | null;
	/** The reply's changes, or 0 when it changed nothing. */
	batch_id: number;
	/** Set on a reply the provider couldn't give. */
	error: string;
}

/** The response to every write. */
export interface Mutation {
	batch_id: number;
}

export interface BatchApplied {
	batch_id: number;
	source: Source;
	activity: Activity[];
}

/** One change in a batch. before and after are the entity's JSON, or null where it didn't exist. */
export interface Activity {
	action: string;
	entity: string;
	entity_id: number;
	before: { status?: Status } | null;
	after: { status?: Status } | null;
}

export interface ProviderConfig {
	main_model: string;
	light_model: string;
	base_url: string;
}

export interface Settings {
	provider: Provider | '';
	anthropic: ProviderConfig;
	openai: ProviderConfig;
	claudecode: ProviderConfig;
	copilot: ProviderConfig;
	style: string;
	about_me: string;
	end_of_day: string;
	nudges: NudgeToggles;
	alert_lead_minutes: number;
	theme: Theme;
	onboarded: boolean;
	allowed_hosts: string[];
}

export type Theme = '' | 'plain' | 'neon' | 'terminal' | 'paper' | 'vaporwave';

export interface NudgeToggles {
	before_event: boolean;
	event_end: boolean;
	late_day: boolean;
}

export interface SettingsView {
	settings: Settings;
	keys: Record<Provider, KeySource>;
}

export interface ConnectionResult {
	ok: boolean;
	error?: string;
}

export type FieldKind = 'text' | 'secret' | 'list';

/** A settings field an integration declares. Only a list has fields, its columns. */
export interface Field {
	key: string;
	label: string;
	kind: FieldKind;
	help?: string;
	required: boolean;
	fields?: Field[];
}

/** A list row. Secrets show only whether they're set. */
export interface IntegrationRow {
	id: string;
	values: Record<string, string>;
	secrets: Record<string, boolean>;
}

/** A series, or any pulled item, the user chose never to see. */
export interface Skip {
	key: string;
	title: string;
}

export interface Integration {
	id: string;
	name: string;
	fields: Field[];
	enabled: boolean;
	/** Every required field is filled, so it can pull once enabled. */
	ready: boolean;
	values: Record<string, string>;
	secrets: Record<string, boolean>;
	lists: Record<string, IntegrationRow[]>;
	skipped: Skip[];
	/** Whether a pull writes events or offers tickets to pick. */
	pulls: 'events' | 'tickets';
}

/** What saving an integration sends. A blank secret keeps the saved one; a row with no id is new. */
export interface IntegrationUpdate {
	values: Record<string, string>;
	lists: Record<string, { id: string; values: Record<string, string> }[]>;
}

export interface PulledEvent {
	id: number;
	title: string;
	starts_at: string;
	ends_at: string | null;
	/** Set on an occurrence of a recurring event, which removing offers to skip for good. */
	series: string;
}

/** A pulled ticket, which becomes a task only when picked. */
export interface Ticket {
	key: string;
	title: string;
	notes: string;
	url: string;
	/** Asked for again, such as a re-requested review, so a done or dropped task with its key doesn't hide it. */
	rereview?: boolean;
}

/** What one source, such as a calendar, pulled. A failed source has an error and nothing else. */
export interface PullSource {
	label: string;
	error?: string;
	events: PulledEvent[];
	tickets: Ticket[];
	/** How many more tickets matched than were returned. */
	more: number;
}

export interface PullResult {
	batch_id: number;
	sources: PullSource[];
}

export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string
	) {
		super(message);
	}
}

// Every write sends a JSON body, even an empty one: the server rejects any other write as a
// possible cross-site form post, and decodes a body on most writes.
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const init: RequestInit = { method };
	if (method !== 'GET') {
		init.headers = { 'Content-Type': 'application/json' };
		init.body = JSON.stringify(body ?? {});
	}
	const res = await fetch(`/api/v1${path}`, init);
	if (res.status === 204) return undefined as T;
	const data = await res.json().catch(() => ({}));
	if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText);
	return data as T;
}

export const api = {
	today: () => request<Today>('GET', '/today'),
	events: (date: string) => request<CalendarEvent[]>('GET', `/events?date=${date}`),
	deleteEvent: (id: number) => request<Mutation>('DELETE', `/events/${id}`),
	tasks: () => request<Task[]>('GET', '/tasks'),
	chat: () => request<Message[]>('GET', '/chat'),
	capture: (raw_text: string) => request<Mutation>('POST', '/tasks', { raw_text }),
	/** A task from the form. With no raw text, cleanup never runs on it. */
	createTask: (fields: TaskFields) => request<Mutation & { data: Task }>('POST', '/tasks', fields),
	setStatus: (id: number, status: Status) =>
		request<Mutation>('POST', `/tasks/${id}/status`, { status }),
	push: (id: number, until: string) => request<Mutation>('POST', `/tasks/${id}/push`, { until }),
	/** Puts a task in a tier of today's plan, or out of it with null. There's no undo. */
	move: (id: number, tier: Tier | null) => request<Mutation>('POST', `/tasks/${id}/move`, { tier }),
	undo: (batch: number) => request<void>('POST', `/batches/${batch}/undo`),
	setPace: (pace: number) => request<Mutation>('PUT', '/today/pace', { pace, note: '' }),
	replan: () => request<Plan>('POST', '/today/replan'),
	updateTask: (id: number, fields: TaskFields) => request<Mutation>('PUT', `/tasks/${id}`, fields),
	blockers: () => request<Blocker[]>('GET', '/blockers'),
	addBlocker: (id: number, blocked_by_id: number) =>
		request<Mutation>('POST', `/tasks/${id}/blockers`, { blocked_by_id }),
	removeBlocker: (id: number, blockedBy: number) =>
		request<Mutation>('DELETE', `/tasks/${id}/blockers/${blockedBy}`),
	morning: () => request<{ review: Review | null }>('GET', '/morning'),
	start: (pace: number, mood_note: string, triage: Triage[], dump: string) =>
		request<{ day: Day; plan: Plan }>('POST', '/today/start', { pace, mood_note, triage, dump }),
	wrapReview: () => request<Review>('GET', '/today/wrap'),
	wrap: (triage: Triage[], note: string) =>
		request<{ batch_id: number; data: Day }>('POST', '/today/wrap', { triage, note }),
	whatNow: (exclude_task_id = 0, reason = '') =>
		request<Pick>('POST', '/whatnow', { exclude_task_id, reason }),
	send: (text: string) => request<Message>('POST', '/chat', { text }),
	settings: () => request<SettingsView>('GET', '/settings'),
	saveSettings: (s: Settings) => request<SettingsView>('PUT', '/settings', s),
	saveKey: (provider: Provider, key: string) =>
		request<void>('PUT', `/settings/keys/${provider}`, { key }),
	testConnection: () => request<ConnectionResult>('POST', '/settings/test'),
	deleteData: () => request<void>('DELETE', '/data'),
	integrations: () => request<Integration[]>('GET', '/integrations'),
	saveIntegration: (id: string, u: IntegrationUpdate) => request<Integration>('PUT', `/integrations/${id}`, u),
	enableIntegration: (id: string, enabled: boolean) =>
		request<Integration>('PUT', `/integrations/${id}/enabled`, { enabled }),
	pull: (id: string) => request<PullResult>('POST', `/integrations/${id}/pull`),
	addTickets: (id: string, tickets: Ticket[]) => request<Mutation>('POST', `/integrations/${id}/add`, { tickets }),
	skip: (id: string, key: string, title: string, event_id?: number) =>
		request<Mutation>('POST', `/integrations/${id}/skipped`, { key, title, event_id }),
	unskip: (id: string, key: string) =>
		request<Integration>('DELETE', `/integrations/${id}/skipped/${encodeURIComponent(key)}`),
	clock: () => request<{ now: string }>('GET', '/clock')
};

/** Whether a model is set up. Without one, capture still works and plans come from code. */
export function configured(view: SettingsView): boolean {
	const p = view.settings.provider;
	return p !== '' && view.keys[p] !== '';
}
