// Deprecated placeholder to prevent 404s on cached browser sessions.
export async function scenariosPage(ctx) {
  if (ctx && ctx.content) {
    ctx.content.innerHTML = '<div class="empty">该功能已下线</div>';
  }
}
