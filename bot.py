import asyncio
import uuid
import json
import logging
import sqlite3
import base64
import os
from aiogram import Bot, Dispatcher, types
from aiogram.filters import Command
from aiogram.types import Message
import aiohttp
from aiohttp import web
from dotenv import load_dotenv

# Load environment variables
load_dotenv()

# ================= CONFIG =================
BOT_TOKEN = os.getenv("BOT_TOKEN")
ADMIN_ID = int(os.getenv("ADMIN_ID", 0))

# Порт для подписки (должен быть открыт в Firewall/Security Groups Яндекса!)
SUB_PORT = int(os.getenv("SUB_PORT", 8000))

# СЕРВЕР А (Яндекс / Каскад)
SERVER_A = {
    "base_url": os.getenv("SERVER_A_BASE_URL"),
    "web_path": os.getenv("SERVER_A_WEB_PATH"),
    "username": os.getenv("SERVER_A_USERNAME"),
    "password": os.getenv("SERVER_A_PASSWORD"),
    "inbound_id": int(os.getenv("SERVER_A_INBOUND_ID", 1)),
    "limit_bytes": int(os.getenv("SERVER_A_LIMIT_BYTES", 30 * 1024 * 1024 * 1024)),
    "public_key": os.getenv("SERVER_A_PUBLIC_KEY"),
    "sni": os.getenv("SERVER_A_SNI"),
    "sid": os.getenv("SERVER_A_SID")
}

# СЕРВЕР Б (Европа / Прямой)
SERVER_B = {
    "base_url": os.getenv("SERVER_B_BASE_URL"),
    "web_path": os.getenv("SERVER_B_WEB_PATH"),
    "username": os.getenv("SERVER_B_USERNAME"),
    "password": os.getenv("SERVER_B_PASSWORD"),
    "inbound_id": int(os.getenv("SERVER_B_INBOUND_ID", 1)),
    "limit_bytes": int(os.getenv("SERVER_B_LIMIT_BYTES", 0)),
    "public_key": os.getenv("SERVER_B_PUBLIC_KEY"),
    "sni": os.getenv("SERVER_B_SNI"),
    "sid": os.getenv("SERVER_B_SID")
}
# ==========================================

logging.basicConfig(level=logging.INFO)
bot = Bot(token=BOT_TOKEN)
dp = Dispatcher()

# --- База данных (храним пользователей) ---
DB_PATH = os.getenv("DB_PATH", "/app/data/users.db")

def init_db():
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    c.execute('''CREATE TABLE IF NOT EXISTS users (email text, uuid text)''')
    conn.commit()
    conn.close()

def add_user_to_db(email, uuid_str):
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    c.execute("INSERT INTO users VALUES (?, ?)", (email, uuid_str))
    conn.commit()
    conn.close()

def get_user_uuid(email):
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    c.execute("SELECT uuid FROM users WHERE email=?", (email,))
    res = c.fetchone()
    conn.close()
    return res[0] if res else None

# --- Класс работы с панелью ---
class ThreeXUI:
    def __init__(self, config):
        self.base_url = config["base_url"]
        self.web_path = config["web_path"].rstrip('/')
        self.username = config["username"]
        self.password = config["password"]
        self.session = None
        self.headers = {
            'User-Agent': 'Mozilla/5.0',
            'Accept': 'application/json, text/plain, */*',
            'X-Requested-With': 'XMLHttpRequest'
        }

    async def get_client_traffic(self, inbound_id, email):
        """Получает трафик через общий список (самый надежный метод)"""
        # Используем LIST, так как GET/{id} часто не содержит stats
        url = f"{self.base_url}{self.web_path}/panel/api/inbounds/list"
        
        try:
            async with self.session.get(url) as resp:
                if resp.status == 200:
                    data = await resp.json()
                    if data.get('success'):
                        inbounds = data.get('obj', [])
                        
                        # 1. Ищем наш инбаунд по ID
                        target_inbound = None
                        for inbound in inbounds:
                            if inbound.get('id') == inbound_id:
                                target_inbound = inbound
                                break
                        
                        if not target_inbound:
                            logging.warning(f"⚠️ Инбаунд ID={inbound_id} не найден в списке!")
                            return 0

                        # 2. Достаем статистику клиентов
                        client_stats = target_inbound.get('clientStats', [])
                        if not client_stats:
                            logging.info(f"ℹ️ У инбаунда {inbound_id} пока нет статистики (пустой clientStats).")
                            return 0

                        # 3. Ищем нужного клиента по email
                        for client in client_stats:
                            if client.get('email') == email:
                                up = client.get('up', 0)
                                down = client.get('down', 0)
                                total = up + down
                                logging.info(f"✅ Нашел трафик для {email}: {total} байт")
                                return total
                        
                        logging.info(f"ℹ️ Клиент {email} есть в настройках, но еще не начал потреблять трафик.")
                        return 0
                else:
                    logging.error(f"Ошибка API (Status {resp.status}) при запросе list")
                    return 0
        except Exception as e:
            logging.error(f"Ошибка получения статистики: {e}")
            return 0

    async def login(self):
        jar = aiohttp.CookieJar(unsafe=True)
        connector = aiohttp.TCPConnector(ssl=False)
        if self.session is None or self.session.closed:
            self.session = aiohttp.ClientSession(connector=connector, headers=self.headers, cookie_jar=jar)
        
        url = f"{self.base_url}{self.web_path}/login"
        payload = {"username": self.username, "password": self.password}
        try:
            async with self.session.post(url, data=payload) as resp:
                return resp.status == 200
        except Exception as e:
            logging.error(f"Login Error: {e}")
            return False

    async def add_client(self, inbound_id, email, uuid_str, limit_bytes):
        url = f"{self.base_url}{self.web_path}/panel/api/inbounds/addClient"
        client_settings = {
            "clients": [{
                "id": uuid_str, "email": email, "flow": "xtls-rprx-vision",
                "totalGB": limit_bytes, "expiryTime": 0, "enable": True, "tgId": "", "subId": ""
            }]
        }
        payload = {"id": inbound_id, "settings": json.dumps(client_settings)}
        try:
            async with self.session.post(url, json=payload) as resp:
                if resp.status == 200:
                    res = await resp.json()
                    return res.get('success'), res.get('msg')
                return False, f"HTTP {resp.status}"
        except Exception as e:
            return False, str(e)

    async def close(self):
        if self.session: await self.session.close()

api_a = ThreeXUI(SERVER_A)
api_b = ThreeXUI(SERVER_B)

def get_email_by_uuid(uuid_str):
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    c.execute("SELECT email FROM users WHERE uuid=?", (uuid_str,))
    res = c.fetchone()
    conn.close()
    return res[0] if res else "User"

# --- Генерация конфигов (динамическая) ---
def generate_vless_links(uuid_str, email):
    ip_a = SERVER_A['base_url'].split('//')[1].split(':')[0]
    ip_b = SERVER_B['base_url'].split('//')[1].split(':')[0]

    # Ссылка А (Россия)
    link_a = (f"vless://{uuid_str}@{ip_a}:443"
              f"?security=reality&encryption=none&pbk={SERVER_A['public_key']}"
              f"&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni={SERVER_A['sni']}&sid={SERVER_A['sid']}"
              f"&spx=%2F#🇷🇺 {email} | 30GB")
    
    # Ссылка Б (Германия / Франкфурт)
    # Поставили флаг 🇩🇪 и подпись FR
    link_b = (f"vless://{uuid_str}@{ip_b}:443"
              f"?security=reality&encryption=none&pbk={SERVER_B['public_key']}"
              f"&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni={SERVER_B['sni']}&sid={SERVER_B['sid']}"
              f"&spx=%2F#🇩🇪 {email} | FR")
    
    return link_a, link_b

# --- Web Server для подписки ---
async def handle_subscription(request):
    try:
        client_uuid = request.match_info.get('uuid')
        logging.info(f"📥 Запрос подписки: {client_uuid}")

        # 1. Ищем имя клиента (email)
        client_name = get_email_by_uuid(client_uuid)

        # 2. Генерируем ссылки
        link_a, link_b = generate_vless_links(client_uuid, client_name)
        config_text = f"{link_a}\n{link_b}"
        config_base64 = base64.b64encode(config_text.encode('utf-8')).decode('utf-8')

        # 3. ПОЛУЧАЕМ ТРАФИК С СЕРВЕРА А (Где лимиты)
        # Обязательно логинимся, так как старая сессия могла протухнуть
        await api_a.login()
        used_traffic = await api_a.get_client_traffic(SERVER_A['inbound_id'], client_name)
        
        limit = SERVER_A['limit_bytes']
        
        # 4. Формируем заголовки с реальной инфой
        headers = {
            "Content-Disposition": 'attachment; filename="fus1ond-VPN"',
            "Profile-Title": "fus1ond-VPN",
            "Profile-Update-Interval": "1", # Обновлять каждый час (если клиент умный)
            # upload=0, потому что мы суммировали всё в download для простоты отображения
            # v2rayTun обычно смотрит на (upload + download) vs total
            "Subscription-Userinfo": f"upload=0; download={used_traffic}; total={limit}; expire=0"
        }
        
        logging.info(f"📊 Отдаем стату для {client_name}: {used_traffic / 1024 / 1024:.2f} MB")
        
        return web.Response(text=config_base64, content_type='text/plain', headers=headers)
    
    except Exception as e:
        logging.error(f"❌ Web Error: {e}")
        return web.Response(text=f"Error", status=500)

async def start_web_server():
    app = web.Application()
    app.router.add_get('/sub/{uuid}', handle_subscription)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, '0.0.0.0', SUB_PORT)
    await site.start()
    logging.info(f"🌍 Web server started on port {SUB_PORT}")

# --- Bot Handlers ---
@dp.message(Command("start"))
async def cmd_start(message: Message):
    if message.from_user.id != ADMIN_ID: return
    await message.answer("🚀 Бот + Подписка активны.\n/create <name>")

@dp.message(Command("create"))
async def cmd_create(message: Message):
    if message.from_user.id != ADMIN_ID: return
    args = message.text.split()
    if len(args) < 2: return await message.answer("Укажи имя!")
    
    email = args[1]
    client_uuid = str(uuid.uuid4())
    
    status = await message.answer(f"⏳ Создаю <b>{email}</b>...", parse_mode="HTML")

    await api_a.login()
    await api_b.login()
    
    ok_a, msg_a = await api_a.add_client(SERVER_A['inbound_id'], email, client_uuid, SERVER_A['limit_bytes'])
    ok_b, msg_b = await api_b.add_client(SERVER_B['inbound_id'], email, client_uuid, SERVER_B['limit_bytes'])

    if ok_a and ok_b:
        # Сохраняем в БД
        add_user_to_db(email, client_uuid)
        
        # Генерируем ССЫЛКУ НА ПОДПИСКУ
        # Берем IP из конфига, убираем https:// и порт
        my_ip = SERVER_A['base_url'].split('//')[1].split(':')[0]
        sub_link = f"http://{my_ip}:{SUB_PORT}/sub/{client_uuid}"

        await status.edit_text(
            f"✅ <b>Клиент {email} создан!</b>\n\n"
            f"🔗 <b>Ссылка-подписка (Вставить в приложение):</b>\n<code>{sub_link}</code>\n\n"
            f"Теперь клиенту достаточно добавить эту ссылку 1 раз.\n"
            f"Если ты поменяешь настройки в боте — они обновятся у клиента.",
            parse_mode="HTML"
        )
    else:
        # Тут тоже лучше HTML, чтобы спецсимволы в ошибках не ломали бота
        await status.edit_text(f"❌ Ошибка:\nRU: {msg_a}\nEU: {msg_b}")

async def main():
    init_db()
    # Запускаем веб-сервер и бота параллельно
    await asyncio.gather(
        start_web_server(),
        dp.start_polling(bot)
    )

if __name__ == "__main__":
    asyncio.run(main())