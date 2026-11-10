#!/usr/bin/env bash
# Carga turnos de ejemplo para la demo, por el mismo camino que usa el gateway
# del portal (cabeceras X-Patient-Id / X-User-Role directo a Turnos). No usa
# contrasenas ni la API de socios, asi que no factura a ningun consumidor.
# Ejecutar UNA vez en el servidor: TURNOS_URL=http://127.0.0.1:8081 ./seed-demo-appointments.sh
set -u
BASE=${TURNOS_URL:-http://127.0.0.1:8081}

# paciente medico dias_desde_hoy indice_de_horario
BOOKINGS=(
  "pat-001 doc-001 3 0"  "pat-001 doc-003 5 2"  "pat-001 doc-004 10 3"
  "pat-003 doc-002 4 1"  "pat-004 doc-004 3 0"  "pat-004 doc-003 9 0"
  "pat-007 doc-002 6 3"  "pat-005 doc-001 3 1"  "pat-005 doc-004 7 4"
  "pat-008 doc-003 4 1"  "pat-008 doc-001 8 2"  "pat-008 doc-002 11 1"
)
CANCEL_LAST_OF=("pat-008 doc-001")

book() { # patient doctor days idx
  local p=$1 d=$2 n=$3 k=$4 day slots start id
  for extra in 0 1 2 3 4 5 6; do # salta fines de semana / dias sin horarios
    day=$(date -d "+$((n + extra)) day" +%F)
    slots=$(curl -s "$BASE/doctors/$d/availability?date=$day" | grep -o '"startAt":"[^"]*"' | cut -d'"' -f4)
    [ -n "$slots" ] && break
  done
  [ -z "$slots" ] && { echo "SIN HORARIOS $p $d"; return; }
  start=$(echo "$slots" | sed -n "$((k + 1))p"); [ -z "$start" ] && start=$(echo "$slots" | head -1)
  resp=$(curl -s -w ' HTTP %{http_code}' -X POST "$BASE/appointments" \
    -H "X-Patient-Id: $p" -H "X-User-Role: patient" -H 'Content-Type: application/json' \
    -d "{\"patientId\":\"$p\",\"doctorId\":\"$d\",\"startAt\":\"$start\"}")
  echo "$p $d $start -> ${resp: -8}"
  LAST_ID=$(echo "$resp" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
}

for b in "${BOOKINGS[@]}"; do
  set -- $b
  book "$1" "$2" "$3" "$4"
  for c in "${CANCEL_LAST_OF[@]}"; do
    if [ "$c" = "$1 $2" ] && [ -n "${LAST_ID:-}" ]; then
      curl -s -o /dev/null -w "cancelado $LAST_ID -> HTTP %{http_code}\n" -X POST "$BASE/appointments/$LAST_ID/cancel" \
        -H "X-Patient-Id: $1" -H "X-User-Role: patient" -H 'Content-Type: application/json' -d '{}'
    fi
  done
done
