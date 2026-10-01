import http from 'k6/http';
import { check, group } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8980';
const EMAIL = __ENV.EMAIL || 'loadtest@example.com';
const PASSWORD = __ENV.PASSWORD || 'loadtest-password-1';

export const options = {
  // 一気に高負荷をかけず、段階的に増やして劣化の始まる地点を探す。
  stages: [
    { duration: '30s', target: 10 },
    { duration: '30s', target: 50 },
    { duration: '30s', target: 100 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    // 平均ではなく p95 / p99 を見る。
    // 平均が速くても、一部の利用者だけ極端に遅いことがある。
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    http_req_failed: ['rate<0.01'],
  },
};

// k6 は setup を全 VU の開始前に1回だけ実行する。
export function setup() {
  http.post(`${BASE_URL}/users`, JSON.stringify({ email: EMAIL, password: PASSWORD }), {
    headers: { 'Content-Type': 'application/json' },
  });

  const login = http.post(`${BASE_URL}/login`, JSON.stringify({ email: EMAIL, password: PASSWORD }), {
    headers: { 'Content-Type': 'application/json' },
  });

  if (login.status !== 204) {
    throw new Error(`login failed: ${login.status} ${login.body}`);
  }

  const cookie = login.cookies['kanban_session'][0].value;

  const project = http.post(`${BASE_URL}/projects`, JSON.stringify({ name: 'load test' }), {
    headers: { 'Content-Type': 'application/json', Cookie: `kanban_session=${cookie}` },
  });

  return { cookie, projectId: JSON.parse(project.body).id };
}

export default function (data) {
  const headers = {
    'Content-Type': 'application/json',
    Cookie: `kanban_session=${data.cookie}`,
  };

  group('list tasks', () => {
    const res = http.get(`${BASE_URL}/projects/${data.projectId}/tasks`, { headers });
    check(res, { 'list is 200': (r) => r.status === 200 });
  });

  group('create task', () => {
    const res = http.post(
      `${BASE_URL}/projects/${data.projectId}/tasks`,
      JSON.stringify({ title: `task ${__VU}-${__ITER}`, priority: 'low' }),
      { headers },
    );
    check(res, { 'create is 201': (r) => r.status === 201 });
  });
}
