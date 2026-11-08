#!/bin/sh
# Crea un usuario por servicio con permisos solo sobre su base (ADR-003).
# Las contrasenas llegan por variables de entorno desde .env; no se guardan en el repositorio.
# MySQL ejecuta este script solo al inicializar un volumen vacio: con un volumen
# existente hay que recrearlo (docker compose down -v) o crear los usuarios a mano.
set -eu

# Duplica las comillas simples para que la contrasena sea un literal SQL valido.
sql_quote() { printf "%s" "$1" | sed "s/'/''/g"; }

TURNOS_PW=$(sql_quote "$TURNOS_DB_PASSWORD")
NOTIF_PW=$(sql_quote "$NOTIF_DB_PASSWORD")

mysql -uroot -p"$MYSQL_ROOT_PASSWORD" <<SQL
CREATE USER IF NOT EXISTS 'turnos'@'%' IDENTIFIED BY '${TURNOS_PW}';
GRANT ALL PRIVILEGES ON turnos.* TO 'turnos'@'%';
CREATE USER IF NOT EXISTS 'notificaciones'@'%' IDENTIFIED BY '${NOTIF_PW}';
GRANT ALL PRIVILEGES ON notificaciones.* TO 'notificaciones'@'%';
FLUSH PRIVILEGES;
SQL
