# Установка

## Предварительные требования

### На роутере

1. **RouterOS 7.4+** — проверить:
   ```
   /system resource print
   ```
   Строка `version:` должна быть ≥ 7.4.

2. **Пакет `container`** — проверить:
   ```
   /system package print
   ```
   Должна быть строка `container`. Если нет — скачать `.npk` с mikrotik.com и установить.

3. **Device-mode `container: yes`**:
   ```
   /system/device-mode/print
   ```
   Если `container: no`:
   ```
   /system/device-mode/update container=yes
   ```
   ⚠️ **После команды — физически нажать кнопку reset на роутере в течение 5 минут.**

4. **USB-флешка** (рекомендуется):
   - Отформатировать в FAT32 или ext4
   - Вставить в роутер
   - Проверить: `/disk print` — должна быть с флагом `M`

## Настройка сети

### 1. veth-интерфейс

```
/interface/veth add name=veth1 address=172.17.0.2/24 gateway=172.17.0.1
/ip address add address=172.17.0.1/24 interface=veth1
```

### 2. NAT

```
/ip firewall nat add chain=srcnat src-address=172.17.0.0/24 action=masquerade
```

### 3. Firewall — разрешить контейнеру API

```
/ip firewall filter add chain=input src-address=172.17.0.2/32 protocol=tcp dst-port=8728 action=accept comment="container to API" place-before=0
```

### 4. Firewall — разрешить LAN доступ к API

⚠️ **Замените `192.168.88.0/24`** на вашу LAN-подсеть.

```
/ip firewall filter add chain=input src-address=192.168.88.0/24 protocol=tcp dst-port=8728 action=accept comment="LAN to API" place-before=0
```

### 5. Ограничить API по адресу

⚠️ **Замените `192.168.88.0/24`** на вашу LAN-подсеть.

```
/ip service set api address=192.168.88.0/24,172.17.0.0/24
```

### 6. Пользователь для API

```
/user group add name=panel policy=read,write,api,rest-api,test
/user add name=api_user group=panel password=CHANGE_ME_STRONG_PASSWORD
```

## Настройка контейнера

### 1. tmpdir на флешку

```
/container/config set registry-url=https://registry-1.docker.io tmpdir=usb1-part1/pull
```

### 2. Env-переменные

⚠️ **Замените значения** на свои:

```
/container/envs add list=panel-env key=PANEL_USER value=admin
/container/envs add list=panel-env key=PANEL_PASS value="CHANGE_ME"
/container/envs add list=panel-env key=JWT_SECRET value="CHANGE_ME_32_RANDOM_CHARS"
/container/envs add list=panel-env key=MIKROTIK_ADDR value=172.17.0.1:8728
/container/envs add list=panel-env key=MIKROTIK_USER value=api_user
/container/envs add list=panel-env key=MIKROTIK_PASS value="CHANGE_ME_API_PASSWORD"
/container/envs add list=panel-env key=DATA_FILE value=/data/panel.json
```

### 3. Создать и запустить контейнер

```
/container add file=usb1-part1/panel.tar interface=veth1 root-dir=disk1/panel envlist=panel-env
/container start [find where name="panel"]
```

### 4. Автозапуск

```
/container set [find where name="panel"] start-on-boot=yes logging=yes restart-policy=always memory-high=20MiB memory-max=30MiB
```

### 5. Проброс порта 8000

⚠️ **Замените `home`** на имя вашего LAN-интерфейса.

```
/ip firewall nat add chain=dstnat in-interface=home protocol=tcp dst-port=8000 action=dst-nat to-addresses=172.17.0.2 to-ports=8000 comment="LAN to panel"
```

## Проверка

1. Открыть в браузере: `http://<IP-роутера>:8000`
2. Войти с логином/паролем из env
3. Проверить вкладки

## Обновление

```
# 1. Остановить и удалить контейнер
/container stop [find where name="panel"]
/container remove [find where name="panel"]
/file remove [find where name~"disk1/panel"]

# 2. Скопировать новый panel.tar на флешку

# 3. Создать заново
/container add file=usb1-part1/panel.tar interface=veth1 root-dir=disk1/panel envlist=panel-env
/container start [find where name="panel"]
/container set [find where name="panel"] start-on-boot=yes logging=yes restart-policy=always
```

⚠️ **Env-переменные сохранятся** — они в отдельной таблице `envs`.