/** RequestStatus separates a missing conversation from an operation failure. */
export const RequestStatus = Object.freeze({
  /** Ready supplies the response data. */
  Ready: 'ready',
  /** MissingRun reports that the requested run no longer exists. */
  MissingRun: 'missing_run',
});

export function createRequest(onMissingRun) {
  return async function request(url, body) {
    const runId = body?.runId || new URL(url, location.href).searchParams.get('runId');
    const response = await fetch(url, body ? {method: 'POST',
      headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)} : {});
    const data = await response.json();
    if (response.status === 404 && runId) {
      onMissingRun(runId);
      return {status: RequestStatus.MissingRun};
    }
    if (!response.ok) throw new Error(data.detail || `Request failed (${response.status})`);
    return {status: RequestStatus.Ready, data};
  };
}
