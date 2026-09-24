import { Readable } from 'node:stream';

export function forwardBody(body, response) {
  if (!body) { response.end(); return; }
  const stream = Readable.fromWeb(body);
  // Aborting a cancelled model response raises on this Node stream as well as
  // rejecting fetch. Always observe it so cleanup/accounting can still run.
  stream.on('error', () => response.destroy());
  stream.pipe(response);
}
