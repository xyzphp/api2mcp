(function () {
  'use strict';
  const form = document.getElementById('login-form');
  const token = document.getElementById('login-token');
  const error = document.getElementById('login-error');
  const submit = document.getElementById('login-submit');
  const appBasePath = new URL(document.querySelector('base')?.href || '/', window.location.origin).pathname.replace(/\/$/, '');
  const appPath = route => `${appBasePath}${route.startsWith('/') ? route : `/${route}`}`;
  document.getElementById('show-login-token').addEventListener('change', event => {
    token.type = event.target.checked ? 'text' : 'password';
  });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (submit.disabled) return;
    error.hidden = true; submit.disabled = true; submit.textContent = '正在登录…';
    try {
      const response = await fetch(appPath('/api/login'), {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: token.value }),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || '登录失败，请重试。');
      token.value = '';
      window.location.replace(appPath('/'));
    } catch (failure) {
      error.textContent = failure.message === 'Failed to fetch' ? '无法连接服务，请检查 Go 服务是否已启动。' : failure.message;
      error.hidden = false; token.focus();
    } finally { submit.disabled = false; submit.textContent = '登录后台 →'; }
  });
})();
