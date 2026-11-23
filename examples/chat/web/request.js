/** RequestStatus separates a missing conversation from an operation failure. */
export const RequestStatus = Object.freeze({
  /** Ready supplies the response data. */
  Ready: 'ready',
  /** MissingRun reports that the requested run no longer exists. */
  MissingRun: 'missing_run',
  /** Superseded ignores a response for a previous selected chat or server state. */
  Superseded: 'superseded',
  /** ServerReset reports a changed server identity and clears the old browser state. */
  ServerReset: 'server_reset',
});

export function createRequest(onMissingRun, onServerReset = () => {}) {
  let serverId = '', generation = 0;
  async function request(url, body) {
    const current = generation;
    const runId = body?.runId || new URL(url, location.href).searchParams.get('runId');
    const headers = {'Content-Type': 'application/json'};
    if (serverId) headers['X-Gent-Server'] = serverId;
    const response = await fetch(url, body ? {method: 'POST',
      headers, body: JSON.stringify(body)} : {});
    const data = await response.json();
    if (current !== generation) return {status: RequestStatus.Superseded};
    const next = response.headers.get('X-Gent-Server');
    if (next && serverId && next !== serverId) {
      serverId = next;
      generation++;
      onServerReset();
      return {status: RequestStatus.ServerReset};
    }
    if (next) serverId = next;
    if (response.status === 404 && runId) {
      onMissingRun(runId);
      return {status: RequestStatus.MissingRun};
    }
    if (!response.ok) throw new Error(data.detail || `Request failed (${response.status})`);
    return {status: RequestStatus.Ready, data};
  }
  // Selection changes fence every pending read, including inspector requests. The server identity
  // stays valid until a response proves a reset; delayed responses cannot switch it back.
  request.invalidate = () => { generation++; };
  return request;
}
