import { PHASE_DEVELOPMENT_SERVER } from 'next/constants.js';
export default (phase) => ({
  distDir: phase === PHASE_DEVELOPMENT_SERVER ? '.next' : '.next-build',
  serverExternalPackages: ['postgres'],
  devIndicators: false,
});
