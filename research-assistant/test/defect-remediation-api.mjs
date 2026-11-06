import assert from 'node:assert/strict';
const base = process.env.REMEDIATION_BASE_URL || 'http://127.0.0.1:15174';
const me = await fetch(base + '/api/me');
assert.equal(me.status, 200);
const identity = await me.json();
const cookie = me.headers.getSetCookie().map((value) => value.split(';')[0]).join('; ');
const key = 'remediation-' + Date.now();
const payload = { intent: 'hello, describe your capabilities', require_review: true, session_id: identity.session_id };
const post = async (body = payload, requestKey = key, authCookie = cookie) => {
  const response = await fetch(base + '/api/plan', { method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: authCookie, 'Idempotency-Key': requestKey }, body: JSON.stringify(body) });
  return { status: response.status, replayed: response.headers.get('Idempotency-Replayed'), body: await response.text() };
};
let planID;
try {
 const responses = await Promise.all(Array.from({ length: 16 }, () => post()));
 assert.ok(responses.every((response) => response.status === 200), JSON.stringify(responses));
 assert.equal(new Set(responses.map((response) => response.body)).size, 1);
 const plan = JSON.parse(responses[0].body).plan_graph;
 planID = plan.id;
 assert.equal(plan.review.status, 'pending');
 const listed = await fetch(base + '/api/plans', { headers: { Cookie: cookie } }).then((response) => response.json());
 assert.equal(listed.plans.length, 1);
 assert.equal(listed.plans[0].id, planID);
 const events = await fetch(base + '/api/plans/' + planID + '/events', { headers: { Cookie: cookie } }).then((response) => response.json());
 const history = Array.isArray(events) ? events : events.events;
 assert.equal(history.filter((event) => event.event_type === 'plan_created').length, 1);
 const replay = await post(); assert.equal(replay.status, 200); assert.equal(replay.replayed, 'true'); assert.equal(replay.body, responses[0].body);
 assert.equal((await post(payload, key + '-new')).status, 429);
 assert.equal((await post({ ...payload, intent: 'changed' })).status, 409);
 console.log(JSON.stringify({ I01: 'PASS', I02: 'PASS', concurrentRequests: 16, uniquePlans: 1, planCreatedEvents: 1, reviewRequired: true, planID }, null, 2));
} finally {
 if (planID) {
  const response = await fetch(base + '/api/plans/' + planID, { method: 'DELETE', headers: { Cookie: cookie } });
  assert.equal(response.status, 200, await response.text());
  const listed = await fetch(base + '/api/plans', { headers: { Cookie: cookie } }).then((response) => response.json());
  assert.equal(listed.plans.length, 0);
  console.log('Test plan removed; no experiments executed.');
 }
}
