import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    availability_peak: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 200), // target req/s
      timeUnit: '1s',
      duration: __ENV.DURATION || '20s',
      preAllocatedVUs: 20,
      maxVUs: 100,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'], // <1% errors
    http_req_duration: ['p(95)<200'], // 95% under 200ms
  },
};

const BASE_URL = __ENV.TARGET_URL || 'http://turnos-1:8081';
const DOCTORS = ['doc-001', 'doc-002'];
const DATES = ['2026-11-10', '2026-11-11', '2026-11-12'];

export default function () {
  const doc = DOCTORS[Math.floor(Math.random() * DOCTORS.length)];
  const date = DATES[Math.floor(Math.random() * DATES.length)];
  const res = http.get(`${BASE_URL}/doctors/${doc}/availability?date=${date}`);

  check(res, {
    'status is 200': (r) => r.status === 200,
    'has slots': (r) => {
      try {
        const body = JSON.parse(r.body);
        return body && Array.isArray(body.slots);
      } catch (e) {
        return false;
      }
    },
  });
}
