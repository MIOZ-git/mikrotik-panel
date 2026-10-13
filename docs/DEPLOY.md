# Деплой на роутер

## Способ 1: через флешку (рекомендуется)

### 1. Собрать образ на ПК

```powershell
# Windows
.\build.ps1

# Linux/Mac
./build.sh
```

На выходе — `panel.tar` (~10 МБ).

### 2. Скопировать на флешку

1. В RouterOS:
   ```
   /disk eject usb1-part1
   ```
2. Физически вынуть флешку из роутера
3. Вставить в ПК
4. Скопировать `panel.tar` в корень флешки
5. Извлечь безопасно
6. Вставить в роутер

### 3. Проверить

```
/file print where name~"panel.tar"
```

### 4. Создать контейнер

```
/container add file=usb1-part1/panel.tar interface=veth1 root-dir=disk1/panel envlist=panel-env
/container start [find where name="panel"]
/container set [find where name="panel"] start-on-boot=yes logging=yes restart-policy=always memory-high=20MiB memory-max=30MiB
```

### 5. Проверить

```
/container print
```

Ожидаемо: `R` (running).

## Способ 2: через Docker Hub

### 1. Запушить образ

```bash
docker buildx build --platform linux/arm/v7 -t YOUR_USERNAME/mikrotik-panel:armv7 --push .
```

### 2. На роутере

```
/container add remote-image=YOUR_USERNAME/mikrotik-panel:armv7 interface=veth1 root-dir=disk1/panel envlist=panel-env
/container start [find where name="panel"]
```

⚠️ **RouterOS сам скачает образ** — флешка не нужна.

## Способ 3: через HTTP-сервер

### 1. На ПК — раздать файл

```bash
python -m http.server 8080
```

⚠️ **`panel.tar` в той же папке.**

### 2. На роутере — скачать

```
/tool fetch url="http://<IP-ПК>:8080/panel.tar" mode=http dst-path=usb1-part1/panel.tar
```

### 3. Создать контейнер

```
/container add file=usb1-part1/panel.tar interface=veth1 root-dir=disk1/panel envlist=panel-env
/container start [find where name="panel"]
```

## Проверка

```
/container print
/tool fetch url="http://172.17.0.2:8000" mode=http
/log print where topics~"container"
```

## Обновление версии

```routeros
/container stop [find where name="panel"]
/container remove [find where name="panel"]
/file remove [find where name~"disk1/panel"]

# Скопировать новый panel.tar на флешку

/container add file=usb1-part1/panel.tar interface=veth1 root-dir=disk1/panel envlist=panel-env
/container start [find where name="panel"]
/container set [find where name="panel"] start-on-boot=yes logging=yes restart-policy=always
```