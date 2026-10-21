#!/bin/sh
# Crea un usuario por servicio con permisos solo sobre su base (ADR-003).
# Las contrasenas llegan por variables de entorno desde .env; no se guardan en el repositorio.
set -eu
mysql -uroot -p"$MYSQL_ROOT_PASSWORD" <<SQL
CREATE USER IF NOT EXISTS 'turnos'@'%' IDENTIFIED BY '${TURNOS_DB_PASSWORD}';
GRANT ALL PRIVILEGES ON turnos.* TO 'turnos'@'%';
CREATE USER IF NOT EXISTS 'notificaciones'@'%' IDENTIFIED BY '${NOTIF_DB_PASSWORD}';
GRANT ALL PRIVILEGES ON notificaciones.* TO 'notificaciones'@'%';
FLUSH PRIVILEGES;
SQL
