#!/bin/bash
set -euo pipefail

# Скрипт установки Node Exporter на VPS-ноду
# Использование: ./install-node-exporter.sh <IP_ЦЕНТРАЛЬНОГО_СЕРВЕРА>

NODE_EXPORTER_VERSION="1.8.2"
CENTRAL_IP="${1:?Укажите IP центрального сервера: ./install-node-exporter.sh 1.2.3.4}"

echo "=== Установка Node Exporter v${NODE_EXPORTER_VERSION} ==="

# Скачиваем и устанавливаем
cd /tmp
wget -q "https://github.com/prometheus/node_exporter/releases/download/v${NODE_EXPORTER_VERSION}/node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64.tar.gz"
tar xzf "node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64.tar.gz"
sudo mv "node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64/node_exporter" /usr/local/bin/
rm -rf "node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64"*

# Создаём systemd-сервис
cat <<EOF | sudo tee /etc/systemd/system/node_exporter.service
[Unit]
Description=Node Exporter
After=network.target

[Service]
User=nobody
ExecStart=/usr/local/bin/node_exporter

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now node_exporter

# Настройка firewall — порт 9100 только для центрального сервера
if command -v ufw &>/dev/null; then
    sudo ufw allow from "${CENTRAL_IP}" to any port 9100 proto tcp
    sudo ufw reload
    echo "UFW: порт 9100 открыт для ${CENTRAL_IP}"
elif command -v firewall-cmd &>/dev/null; then
    sudo firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=${CENTRAL_IP} port port=9100 protocol=tcp accept"
    sudo firewall-cmd --reload
    echo "firewalld: порт 9100 открыт для ${CENTRAL_IP}"
else
    echo "ВНИМАНИЕ: firewall не найден. Вручную откройте порт 9100 для ${CENTRAL_IP}"
fi

# Проверка (ждём до 15 секунд пока сервис поднимется)
for i in $(seq 1 15); do
    if curl -sf http://localhost:9100/metrics | head -1 | grep -q "HELP"; then
        echo "=== Node Exporter установлен и работает ==="
        exit 0
    fi
    sleep 1
done

echo "ОШИБКА: Node Exporter не отвечает на localhost:9100"
exit 1
