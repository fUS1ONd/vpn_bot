# Прокси до ЮKassa

С 29.10.2026 `api.yookassa.ru` и `yoomoney.ru` не отвечают на TLS с IP сервера бота:
TCP соединяется, рукопожатие висит, в логах бота — `TLS handshake timeout`. С домашнего
сервера те же адреса отвечают сразу.

## Устройство

```text
контейнер бота → ipip-home → 10.255.30.1:1081 (dante на домашнем сервере) → api.yookassa.ru:443
```

Отдельный SSH-туннель не нужен: контейнер достаёт `10.255.30.1` через существующий ipip.
`socks-proxy.service` на домашнем сервере не подходит: он выводит домашний сервер в
интернет через сервер бота, то есть в обратную сторону.

ipip не шифрует трафик, но внутри него идёт TLS до кассы: ключ магазина и данные платежей
закрыты. Адрес назначения в SOCKS виден, это допустимо.

Домашний сервер — единая точка отказа. Отката на прямой путь нет, потому что прямой путь
не работает. В ipip домашний адрес прописан статически: при его смене пропадёт и прокси.

## Настройка

На домашнем сервере:

```bash
sudo apt-get install -y dante-server
sudo cp scripts/danted-yookassa.conf /etc/danted.conf
sudo mkdir -p /etc/systemd/system/danted.service.d
printf '[Service]\nRestart=always\nRestartSec=10\n' | sudo tee /etc/systemd/system/danted.service.d/restart.conf
sudo systemctl daemon-reload
sudo systemctl enable danted
sudo systemctl restart danted
```

`Restart=always` нужен потому, что dante не стартует, пока не поднят ipip-интерфейс.
`external` — интерфейс домашнего сервера с маршрутом в интернет: при `external.rotation: route`
dante отказывает, если маршрут ведёт через интерфейс не из списка. Фильтр `to: api.yookassa.ru`
пропускает и IP кассы, а не только имя: dante сам разрешает имя при сверке.

В `.env` бота: `YOOKASSA_PROXY_URL=socks5://10.255.30.1:1081`, затем пересоздать контейнер.

## Проверка

С сервера бота. Без ключа касса отвечает 401, и это успех: ответ получен, путь работает.

```bash
curl -sS -o /dev/null -w '%{http_code}\n' --socks5-hostname 10.255.30.1:1081 https://api.yookassa.ru/v3/me
```

Обращаться к кассе по голому IP не нужно: она ответила на это 456, и сразу после такой
проверки домашний IP перестал принимать TCP.

Другой хост должен быть отклонён: так проверяется ограничение.

```bash
curl -sS -m 10 --socks5-hostname 10.255.30.1:1081 https://ya.ru
```
