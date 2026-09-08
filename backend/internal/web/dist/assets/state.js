// Shared application state.
export const state = {
  user: null,
  token: null,
  route: null,
  routes: [],
  ws: null,
  connected: false,
  onEvent: null,
  updateConn: () => {},
  stats: {},
  settings: null,
};
