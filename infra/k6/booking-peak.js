import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    booking_peak: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '5s', target: 20 },  // ramp to 20 concurrent VUs
        { duration: '10s', target: 20 }, // hold peak
        { duration: '5s', target: 0 },   // ramp down
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    // We expect either 201 Created or 409 Conflict under heavy contention
    'http_req_duration{status:201}': ['p(95)<500'],
  },
};

const BASE_URL = __ENV.TARGET_URL || 'http://gateway:8088';
const API_KEY = __ENV.API_KEY || 'dev-carcruds-key';
const PATIENTS = ['pat-001', 'pat-003'];

export default function () {
  // 60% of bookings target the exact same slot to test lock contention and 409
  const isContended = Math.random() < 0.6;
  const day = new Date(Date.now() + 3 * 86400000);
  day.setUTCHours(12, 0, 0, 0); // 09:00 ART, doc-001's opening
  while (day.getUTCDay() === 0 || day.getUTCDay() === 6) day.setUTCDate(day.getUTCDate() + 1);
  const startAt = new Date(day.getTime() + (isContended ? 0 : (__VU % 8) * 30 * 60000)).toISOString();

  const payload = JSON.stringify({
    doctorId: 'doc-001',
    patientId: PATIENTS[__VU % PATIENTS.length],
    startAt: startAt,
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': API_KEY,
      'Idempotency-Key': `load-${__VU}-${__ITER}`,
    },
  };

  const res = http.post(`${BASE_URL}/v1/appointments`, payload, params);

  check(res, {
    'valid booking outcome (201, 409 or 429)': (r) => r.status === 201 || r.status === 409 || r.status === 429,
  });

  sleep(0.1);
}
