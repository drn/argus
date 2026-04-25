// Reset the argus-test-server back to a single seeded `echo-bash` task with a
// running bash session. Called from beforeEach in each spec to keep tests
// independent.
export async function resetServer() {
  const port = Number(process.env.ARGUS_TEST_PORT || 7744);
  const r = await fetch(`http://127.0.0.1:${port + 10}/test/reset`, { method: 'POST' });
  if (!r.ok) throw new Error(`reset failed: ${r.status}`);
  return r.json();
}
