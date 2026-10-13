# MikroTik Panel

Веб-панель для управления роутером MikroTik через RouterOS API. Работает как Docker-контейнер прямо на роутере (RouterOS 7.4+).

![Go](https://img.shields.io/badge/Go-1.22-00ADD8?logo=go)
![RouterOS](https://img.shields.io/badge/RouterOS-7.4%2B-blue)
![License](https://img.shields.io/badge/license-MIT-green)

## Возможности

- 📊 **Мониторинг** — CPU, RAM, Flash, USB в реальном времени (круговые диаграммы)
- 📱 **Устройства в сети** — DHCP leases + ARP с фильтром LAN / Wi-Fi
- 📡 **Wi-Fi клиенты** — уровень сигнала (dBm), скорость, интерфейс
- 🔀 **Переключение WAN** — вручную между двумя провайдерами
- 📋 **Логи** — критичные события (critical / error / warning)
- 🔄 **Перезагрузка роутера** — с двойным подтверждением и таймером
- 🏓 **Автопинг** — настраиваемые хосты
- 🔐 **Авторизация** — HMAC-подписанные токены в HttpOnly cookie
- 📱 **Адаптивный UI** — работает на десктопе и мобильных

## Быстрый старт

### 1. Клонировать

```bash
git clone https://github.com/YOUR_USERNAME/mikrotik-panel.git
cd mikrotik-panel
```

### 2. Собрать

```bash
# Linux/Mac
./build.sh

# Windows
.\build.ps1
```

На выходе — `panel.tar` (~10 МБ).

### 3. Задеплоить

См. [docs/DEPLOY.md](docs/DEPLOY.md).

### 4. Открыть

```
http://<IP-роутера>:8000
```

## Требования

### Роутер
- RouterOS **7.4+** (для контейнеров)
- Архитектура **ARM / ARM64 / x86**
- **≥ 64 МБ RAM** свободно
- **USB-флешка** (рекомендуется)

### ПК
- Docker 20+
- Docker buildx

## Конфигурация

Все параметры — через env-переменные контейнера:

| Переменная | Описание | По умолчанию |
|---|---|---|
| `PANEL_USER` | Логин панели | `admin` |
| `PANEL_PASS` | Пароль панели | `admin` |
| `JWT_SECRET` | Секрет для HMAC-подписи | random |
| `MIKROTIK_ADDR` | Адрес API роутера | `172.17.0.1:8728` |
| `MIKROTIK_USER` | Пользователь API | `api_user` |
| `MIKROTIK_PASS` | Пароль API | — |
| `DATA_FILE` | Путь к сохранённому паролю | `/data/panel.json` |
| `PING_HOSTS` | Хосты для автопинга | `8.8.8.8,5.255.255.77` |
| `PING_PERIOD` | Период пинга (сек) | `30` |
| `LISTEN` | Адрес и порт | `:8000` |

⚠️ **Смените `PANEL_USER`, `PANEL_PASS`, `JWT_SECRET`** перед деплоем.

## Архитектура

```
┌─────────────────────────────────────────────┐
│  MikroTik Router                            │
│                                             │
│  ┌──────────────┐       ┌────────────────┐  │
│  │  RouterOS    │◄─────►│  Контейнер     │  │
│  │  API :8728   │       │  Go-панель     │  │
│  └──────────────┘       │  :8000         │  │
│         ▲               └────────────────┘  │
│         │ veth1                             │
│         │                                    │
│  ┌──────┴─────────┐                         │
│  │  LAN           │◄── браузер             │
│  └────────────────┘                         │
└─────────────────────────────────────────────┘
```

## Сборка под другие архитектуры

```bash
# ARM 64-bit
docker buildx build --platform linux/arm64 -t mikrotik-panel:arm64 --load .

# x86 (CHR)
docker buildx build --platform linux/amd64 -t mikrotik-panel:amd64 --load .
```

⚠️ **Не забудьте** изменить `GOARCH` в `Dockerfile` (`arm`, `arm64`, `amd64`).

## Разработка

```bash
export PANEL_USER=admin
export PANEL_PASS=secret
export JWT_SECRET=change-me
export MIKROTIK_ADDR=192.168.88.1:8728
export MIKROTIK_USER=api_user
export MIKROTIK_PASS=apipass

go run .
```

## Безопасность

- ⚠️ **Смените пароли** после первого входа
- ⚠️ **Сгенерируйте `JWT_SECRET`** — 32+ случайных символа
- ⚠️ **API-пользователь** — минимальные права (`read,write,api`)
- ⚠️ **Не открывайте порт 8000** в интернет
- ⚠️ **HTTPS** — если панель доступна из недоверенной сети

## Документация

- [Установка](docs/INSTALL.md)
- [Деплой](docs/DEPLOY.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)

## Лицензия

MIT — см. [LICENSE](LICENSE).

## Вклад

Pull requests приветствуются. Для больших изменений — откройте issue для обсуждения.