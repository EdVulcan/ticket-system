// Public runtime settings only. Never put an app secret in the mini program.
// Production builds must provide an HTTPS API prefix and the mini program AppID.
module.exports = Object.freeze({
  deploymentMode: 'production',
  apiBaseUrl: 'https://ymsq.edvulcan.top/api/v1',
  appId: 'wx773349253c56db94',
  requestTimeoutMs: 10000
});
