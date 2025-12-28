import asyncio
import uuid
import json
import logging
from aiogram import Bot, Dispatcher, types
from aiogram.filters import Command
from aiogram.types import Message
import aiohttp

# ================= CONFIG =================
BOT_TOKEN = "ТВОЙ_ТОКЕН_ОТ_BOTFATHER"
ADMIN_ID = 123456789  # Твой цифровой ID в Telegram (чтобы никто другой не создавал ключи)

# Данные СЕРВЕРА А (Яндекс / Каскад / Лимит)
SERVER_A = {
    "url": "http://IP_SERVER_A:2053",  # Без слеша в конце
    "username": "admin",
    "password": "password",
    "inbound_id": 1,  # ID инбаунда VLESS на этом сервере (обычно 1, если он первый)
    "limit_bytes": 30 * 1024 * 1024 * 1024,  # 30 GB в байтах
    "public_key": "ВАШ_REALITY_PUBLIC_KEY_A", # Для генерации ссылки
    "sni": "music.yandex.ru",                 # SNI для ссылки
    "sid": "short_id_a"                       # Short ID для ссылки
}

# Данные СЕРВЕРА Б (Европа / Прямой / Безлимит)
SERVER_B = {
    "url": "http://IP_SERVER_B:2053",
    "username": "admin",
    "password": "password",
    "inbound_id": 1,
    "limit_bytes": 0,  # 0 = Безлимит
    "public_key": "ВАШ_REALITY_PUBLIC_KEY_B",
    "sni": "www.google.com", # Или что там у вас настроено
    "sid": "short_id_b"
}
# ==========================================

logging.basicConfig(level=logging.INFO)
bot = Bot(token=BOT_TOKEN)
dp = Dispatcher()

class ThreeXUI:
    def __init__(self, base_url, username, password):
        self.base_url = base_url
        self.username = username
        self.password = password
        self.session = None

    async def login(self):
        """Авторизация и получение Cookie"""
        if self.session is None:
            self.session = aiohttp.ClientSession()
        
        url = f"{self.base_url}/login"
        payload = {"username": self.username, "password": self.password}
        
        async with self.session.post(url, data=payload) as resp:
            if resp.status == 200:
                data = await resp.json()
                if data.get('success'):
                    logging.info(f"Login success: {self.base_url}")
                    return True
            logging.error(f"Login failed: {self.base_url} -> {await resp.text()}")
            return False

    async def add_client(self, inbound_id, email, uuid_str, limit_bytes):
        """Добавление клиента"""
        url = f"{self.base_url}/panel/api/inbounds/addClient"
        
        # Специфичная структура для 3X-UI: settings передается как JSON-строка внутри JSON-объекта
        client_settings = {
            "clients": [
                {
                    "id": uuid_str,
                    "email": email,
                    "flow": "xtls-rprx-vision", # Для Reality обязателен Vision
                    "totalGB": limit_bytes,
                    "expiryTime": 0,
                    "enable": True,
                    "tgId": "",
                    "subId": ""
                }
            ]
        }
        
        payload = {
            "id": inbound_id,
            "settings": json.dumps(client_settings)
        }
        
        try:
            async with self.session.post(url, json=payload) as resp:
                result = await resp.json()
                if result.get('success'):
                    return True
                logging.error(f"Error adding client to {self.base_url}: {result}")
                return False
        except Exception as e:
            logging.error(f"Exception requesting {self.base_url}: {e}")
            return False

    async def close(self):
        if self.session:
            await self.session.close()

# Инициализация API клиентов
api_a = ThreeXUI(SERVER_A['url'], SERVER_A['username'], SERVER_A['password'])
api_b = ThreeXUI(SERVER_B['url'], SERVER_B['username'], SERVER_B['password'])

@dp.message(Command("start"))
async def cmd_start(message: Message):
    if message.from_user.id != ADMIN_ID:
        return
    await message.answer("Привет! Используй /create <имя_клиента>, чтобы создать ключи.")

@dp.message(Command("create"))
async def cmd_create(message: Message):
    if message.from_user.id != ADMIN_ID:
        return

    # Парсим имя из команды
    args = message.text.split()
    if len(args) < 2:
        await message.answer("Ошибка: введите имя клиента. Пример: /create ivan")
        return
    
    email = args[1]
    # Генерируем общий UUID для обоих серверов
    client_uuid = str(uuid.uuid4())
    
    status_msg = await message.answer("⏳ Создаю клиента на обоих серверах...")

    # 1. Логин (если сессия истекла, метод login обновит куки, но тут упрощенно вызываем перед действием)
    await api_a.login()
    await api_b.login()

    # 2. Добавление клиента
    # Сервер А (Лимит 30ГБ)
    success_a = await api_a.add_client(SERVER_A['inbound_id'], email, client_uuid, SERVER_A['limit_bytes'])
    # Сервер Б (Безлимит)
    success_b = await api_b.add_client(SERVER_B['inbound_id'], email, client_uuid, SERVER_B['limit_bytes'])

    if success_a and success_b:
        # 3. Генерация ссылок (Вручную формируем VLESS string)
        # Формат: vless://UUID@IP:PORT?security=reality&encryption=none&pbk=KEY&headerType=none&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni=SNI&sid=SID#NAME
        
        link_a = (f"vless://{client_uuid}@{SERVER_A['url'].split('//')[1].split(':')[0]}:443"
                  f"?security=reality&encryption=none&pbk={SERVER_A['public_key']}"
                  f"&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni={SERVER_A['sni']}&sid={SERVER_A['sid']}"
                  f"#{email}_RU_30GB")
        
        link_b = (f"vless://{client_uuid}@{SERVER_B['url'].split('//')[1].split(':')[0]}:443"
                  f"?security=reality&encryption=none&pbk={SERVER_B['public_key']}"
                  f"&fp=chrome&type=tcp&flow=xtls-rprx-vision&sni={SERVER_B['sni']}&sid={SERVER_B['sid']}"
                  f"#{email}_EU_UNLIM")

        response_text = (
            f"✅ **Клиент {email} создан!**\n\n"
            f"🆔 UUID: `{client_uuid}`\n\n"
            f"🇷🇺 **RU (Каскад, 30GB):**\n`{link_a}`\n\n"
            f"🇪🇺 **EU (Прямой, NoLimit):**\n`{link_b}`"
        )
        await status_msg.edit_text(response_text, parse_mode="Markdown")
    else:
        err_text = "❌ Ошибка при создании:\n"
        if not success_a: err_text += "- Не удалось добавить на Сервер А (Яндекс)\n"
        if not success_b: err_text += "- Не удалось добавить на Сервер Б (Европа)\n"
        await status_msg.edit_text(err_text)

async def main():
    await dp.start_polling(bot)

if __name__ == "__main__":
    asyncio.run(main())