# Решение проблем

## Контейнер не запускается

### `unknown user panel`

**Причина:** в Dockerfile был `USER panel`, RouterOS не поддерживает это.

**Решение:** убрать `USER panel` из Dockerfile, пересобрать.

### Цикл перезапуска

```
/container set [find where name="panel"] restart-policy=no
/container stop [find where name="panel"]
/log print where topics~"container"
```

### `not enough space`

```
/file print
/file remove [find where name~"^flash/.*\.rsc"]
```

## Панель не открывается

**Проверки:**

```
/container print
/tool fetch url="http://172.17.0.2:8000" mode=http
/ip firewall nat print
/ip firewall filter print chain=forward
```

## 401 Unauthorized

Очистить cookies для `<IP-роутера>`, перезайти.

## Wi-Fi клиенты не показываются

```
/interface wireless registration-table print
```

Если пусто — никого нет в Wi-Fi.

## Сигнал показывает `—`

Проверить формат:

```
/interface wireless registration-table print detail
```

Ожидаемо: `signal-strength=-45@6Mbps` или `-45dBm@6Mbps`.

## Автопинг всегда красный

```
/ping 8.8.8.8 count=1
```

Если timeout — ICMP блокируется провайдером. Сменить хосты в `PING_HOSTS`.

## Переключение WAN не работает

Маршруты динамические (от DHCP) — нельзя `disable`/`enable`.

**Решение:** сделать статические маршруты с разными `distance`.

## Мало RAM

- Ограничить: `memory-max=20MiB`
- Отключить лишние пакеты: `/system package print`
- Перезагрузить роутер

## Сброс пароля панели

```
/container stop [find where name="panel"]
/file remove [find where name~"disk1/panel/data/panel.json"]
/container/envs remove [find key="PANEL_PASS"]
/container/envs add list=panel-env key=PANEL_PASS value="new_password"
/container start [find where name="panel"]
```