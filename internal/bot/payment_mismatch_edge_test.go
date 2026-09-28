package bot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fus1ond/vpn_bot/internal/database"
	"github.com/fus1ond/vpn_bot/internal/paymentprovider"
	"github.com/fus1ond/vpn_bot/internal/platega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Краевые случаи несовпадения ответа провайдера с записью платежа: входы
// (сверка, вебхук ЮKassa, ручная проверка, автосписание) × состояния платежа ×
// данные ответа × отказы × конкуренция.

// yooRawBody — ответ кассы с произвольной строкой суммы, валютой и id: краевым
// случаям нужны значения, которые yooPaymentBody собрать не умеет (копейки).
func yooRawBody(id, status, amount, currency, shop string) string {
	return fmt.Sprintf(`{"id":%q,"status":%q,"amount":{"value":%q,"currency":%q},"recipient":{"account_id":%q}}`,
		id, status, amount, currency, shop)
}

// plategaRawBody — ответ Platega с произвольной валютой и суммой.
func plategaRawBody(id, status string, amount float64, currency string) string {
	return fmt.Sprintf(`{"id":%q,"status":%q,"paymentDetails":{"amount":%v,"currency":%q},"paymentMethod":"SBPQR","expiresIn":"00:00:00"}`,
		id, status, amount, currency)
}

// ---------------------------------------------------------------------------
// Ось данных ответа: какие именно расхождения считаются несовпадением
// ---------------------------------------------------------------------------

// Касса назвала «оплачено» в другой валюте: рубли по записи и доллары в ответе —
// это другие деньги. Без оповещения владелец не узнал бы о списании, по которому
// подписка не выдана.
func TestСверка_ОплатаВДругойВалюте_НеВыдаётПодпискуИОповещаетВладельцаОдинРаз(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooRawBody(id, "succeeded", "400.00", "USD", "shop")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "pending", env.status(t, id))
	assert.Zero(t, env.panel.patchCount())
	alerts := env.tg.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "USD", "владельцу видно, что не сошлась именно валюта")
}

// Касса ответила по чужому идентификатору: подписку за чужой платёж выдавать
// нельзя, а «оплачено» по нему — повод разобраться.
func TestСверка_ОтветСДругимИдентификатором_НеПодтверждаетЧужойПлатёжИОповещает(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, _ string) (int, string) {
		return http.StatusOK, yooRawBody("yoo-someone-else", "succeeded", "400.00", "RUB", "shop")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "pending", env.status(t, id))
	alerts := env.tg.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "yoo-someone-else")
}

// Platega в сверке: «оплачено» с другой суммой. Раньше Platega сверялась только
// в сверке и на вебхуке, теперь та же точка оповещения должна работать и для неё —
// иначе деньги по крипте терялись бы молча.
func TestСверка_PlategaОплаченоНаДругуюСумму_ОповещаетБезСтрокиПолучателя(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 100)
	id := env.pending(t, paymentprovider.Platega, time.Hour)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "pending", env.status(t, id))
	alerts := env.tg.matching(mismatchAlertMarker)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0].Text, "100")
	assert.NotContains(t, alerts[0].Text, "Получатель", "у Platega получатель не сверяется — пустая строка сбивала бы с толку")
}

// Platega ответила «оплачено» в USDT: рубли по записи не сошлись с валютой ответа.
func TestСверка_PlategaОплаченоВДругойВалюте_ЭтоНесовпадение(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, plategaRawBody(id, platega.StatusConfirmed, 400, "USDT")
	}
	id := env.pending(t, paymentprovider.Platega, time.Hour)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "pending", env.status(t, id))
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1)
}

// Дедупликация по платежу, а не общая: два разных несовпавших платежа — два
// разных случая, и второй не должен потонуть в пометке первого.
func TestСверка_ДваРазныхНесовпавшихПлатежа_ДваОповещения(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooRawBody(id, "succeeded", "100.00", "RUB", "shop")
	}
	first := env.pending(t, paymentprovider.YooKassa, time.Hour)
	second := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayment(first, time.Now().UTC(), "test")
	env.bot.reconcilePendingPayment(second, time.Now().UTC(), "test")

	assert.Len(t, env.tg.matching(mismatchAlertMarker), 2)
}

// ---------------------------------------------------------------------------
// Ось времени: несовпадение, которое проходит, и срок в сутки
// ---------------------------------------------------------------------------

// Касса сначала ответила не тем, потом исправилась: оплата должна принять
// нормально. Иначе пометка «уже сообщили» или сам факт несовпадения навсегда
// заблокировали бы честно оплаченный платёж.
func TestСверка_НесовпадениеЗатемСовпадение_ПлатёжПодтверждаетсяОбычнымПутём(t *testing.T) {
	env := newEdgeEnv(t)
	var fixed atomic.Bool
	env.yoo.onGet = func(_ int, id string) (int, string) {
		if fixed.Load() {
			return http.StatusOK, yooPaymentBody(id, "succeeded", "", 400, "shop", "RUB")
		}
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 100, "shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
	require.Equal(t, "pending", env.status(t, id))
	fixed.Store(true)
	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Equal(t, 1, env.panel.patchCount(), "подписка продлена один раз")
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1, "оповещение было одно, повторного нет")
}

// То же на вебхуке: повторная доставка после исправления принимает оплату.
func TestВебхук_НесовпадениеЗатемСовпадение_ПовторнаяДоставкаПринимаетОплату(t *testing.T) {
	env := newEdgeEnv(t)
	var fixed atomic.Bool
	env.yoo.onGet = func(_ int, id string) (int, string) {
		amount := 100
		if fixed.Load() {
			amount = 400
		}
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", amount, "shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	ext := env.externalID(t, id)

	require.NoError(t, env.bot.HandleYooKassaWebhook("payment.succeeded", ext))
	fixed.Store(true)
	require.NoError(t, env.bot.HandleYooKassaWebhook("payment.succeeded", ext))

	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Equal(t, 1, env.panel.patchCount())
}

// То же на кнопке: человек нажал «Я оплатил» после исправления — получил подписку.
func TestРучнаяПроверка_НесовпадениеЗатемСовпадение_ВторойНажатиеВыдаётПодписку(t *testing.T) {
	env := newEdgeEnv(t)
	var fixed atomic.Bool
	env.platega.onGet = func(n int, id string) (int, string) {
		amount := 100
		if fixed.Load() {
			amount = 400
		}
		return plategaSaysAmount(platega.StatusConfirmed, amount)(n, id)
	}
	id := env.pending(t, paymentprovider.Platega, 5*time.Minute)

	first := checkPaymentByButton(t, env)
	fixed.Store(true)
	second := checkPaymentByButton(t, env)

	assert.Contains(t, first, mismatchUserText)
	assert.Equal(t, "confirmed", env.status(t, id))
	assert.Contains(t, second, "Оплата прошла")
}

// Несовпавший pending на кнопке, а потом он же — «оплачено» с несовпадением:
// сначала «пока не поступила», потом текст о проверке и одно оповещение. Пометка,
// поставленная на pending, съела бы оповещение о реальных деньгах.
func TestРучнаяПроверка_НесовпавшийPendingСталОплачено_ОповещениеПриходит(t *testing.T) {
	env := newEdgeEnv(t)
	var paid atomic.Bool
	env.platega.onGet = func(n int, id string) (int, string) {
		status := platega.StatusPending
		if paid.Load() {
			status = platega.StatusConfirmed
		}
		return plategaSaysAmount(status, 100)(n, id)
	}
	env.pending(t, paymentprovider.Platega, 5*time.Minute)

	first := checkPaymentByButton(t, env)
	require.Empty(t, env.tg.matching(mismatchAlertMarker))
	paid.Store(true)
	second := checkPaymentByButton(t, env)

	assert.Contains(t, first, "Оплата пока не поступила")
	assert.Contains(t, second, mismatchUserText)
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1)
}

// Несовпавший pending к сроку: закрывается, но владельца не тревожим — денег нет.
func TestСверка_НесовпавшийНеоплаченныйКСроку_ЗакрываетсяБезОповещения(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "pending", "", 100, "shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)

	env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(24*time.Hour), "test")

	assert.Equal(t, "expired", env.status(t, id))
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Несовпадение, не разрешившееся за сутки, закрывает платёж, как и прежде, а
// закрытие по сроку не глушит оповещение: у обоих провайдеров.
func TestСверка_НесовпадениеКСроку_ЗакрываетсяИОповещает(t *testing.T) {
	cases := map[string]func(env *edgeEnv){
		paymentprovider.YooKassa: func(env *edgeEnv) {
			env.yoo.onGet = func(_ int, id string) (int, string) {
				return http.StatusOK, yooPaymentBody(id, "succeeded", "", 100, "shop", "RUB")
			}
		},
		paymentprovider.Platega: func(env *edgeEnv) {
			env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 100)
		},
	}
	for provider, mismatch := range cases {
		t.Run(provider, func(t *testing.T) {
			env := newEdgeEnv(t)
			mismatch(env)
			id := env.pending(t, provider, 20*time.Minute)

			env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(25*time.Hour), "test")

			assert.Equal(t, "expired", env.status(t, id))
			assert.Len(t, env.tg.matching(mismatchAlertMarker), 1, "закрытие по сроку не глушит оповещение")
		})
	}
}

// Несовпавший «оплачено» за секунду до суток ещё не закрывается: закрыть раньше
// предела — значит закрыть платёж, который касса ещё может исправить.
func TestСверка_НесовпадениеЗаСекундуДоСуток_ПлатёжЕщёЖивёт(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 100, "shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)

	env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(24*time.Hour-time.Second), "test")

	assert.Equal(t, "pending", env.status(t, id))
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1)
}

// ---------------------------------------------------------------------------
// Ось состояний платежа × входы
// ---------------------------------------------------------------------------

// Локально закрытый платёж (бросили ссылку или сменили способ оплаты): деньги
// по нему могли пройти. «Оплачено» с несовпадением — одно оповещение за
// несколько проходов, но не воскрешение.
func TestСверка_ЛокальноЗакрытыйПлатёжСНесовпавшимОплачено_ОповещаетИНеВоскрешает(t *testing.T) {
	for _, status := range []string{"expired", "canceled"} {
		t.Run(status, func(t *testing.T) {
			env := newEdgeEnv(t)
			env.yoo.onGet = func(_ int, id string) (int, string) {
				return http.StatusOK, yooPaymentBody(id, "succeeded", "", 100, "shop", "RUB")
			}
			id := env.pending(t, paymentprovider.YooKassa, time.Hour)
			require.NoError(t, env.db.UpdatePaymentStatus(id, status))

			env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
			env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

			assert.Equal(t, status, env.status(t, id), "по несовпавшему ответу платёж не воскрешается")
			assert.Zero(t, env.panel.patchCount())
			assert.Len(t, env.tg.matching(mismatchAlertMarker), 1)
		})
	}
}

// Вебхук по локально закрытому платежу с несовпадением: тоже оповещение, без
// воскрешения — иначе выдали бы подписку по ответу, который не сошёлся.
func TestВебхук_ЛокальноЗакрытыйПлатёжСНесовпадением_НеВоскрешаетсяИОповещает(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 400, "other-shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	require.NoError(t, env.db.ExpirePendingPayment(id))

	require.NoError(t, env.bot.HandleYooKassaWebhook("payment.succeeded", env.externalID(t, id)))

	assert.Equal(t, "expired", env.status(t, id))
	assert.Zero(t, env.panel.patchCount())
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1)
	assert.Empty(t, env.tg.matching("провайдер подтвердил оплату"), "о воскрешении не сообщаем: его не было")
}

// Вебхук «отменён» с несовпадением: статус из несошедшегося ответа к записи не
// применяется, владельцу денег разбирать нечего, кассе — успех.
func TestВебхук_ОтменаСНесовпадением_НеМеняетЗаписьИНеТревожитВладельца(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "canceled", "", 100, "shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	require.NoError(t, env.bot.HandleYooKassaWebhook("payment.canceled", env.externalID(t, id)))

	assert.Equal(t, "pending", env.status(t, id))
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Platega без provider_payment_id (создание сорвалось до ответа): спросить
// провайдера не о чем — это молчание, а не несовпадение, оповещения быть не должно.
func TestСверка_PlategaБезИдентификатораПровайдера_НеСчитаетсяНесовпадением(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 100)
	id, err := env.db.CreatePayment(&database.Payment{
		TelegramID: edgeUserID, Amount: 400, PaymentMethod: "crypto", Status: "pending",
		Provider: paymentprovider.Platega,
	})
	require.NoError(t, err)
	_, err = env.db.Conn().Exec(`UPDATE payments SET created_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-25*time.Hour).Format(database.SQLiteSecondsLayout), id)
	require.NoError(t, err)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Equal(t, "expired", env.status(t, id))
	assert.Zero(t, env.platega.getCount(), "без идентификатора провайдера не о чем спрашивать")
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Та же запись на ручной проверке: «пока не поступила», без текста о проверке.
func TestРучнаяПроверка_PlategaБезИдентификатора_ПоказываетОжиданиеБезТревоги(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 100)
	_, err := env.db.CreatePayment(&database.Payment{
		TelegramID: edgeUserID, Amount: 400, PaymentMethod: "crypto", Status: "pending",
		Provider: paymentprovider.Platega,
	})
	require.NoError(t, err)

	msg := checkPaymentByButton(t, env)

	assert.Contains(t, msg, "Оплата пока не поступила")
	assert.NotContains(t, msg, mismatchUserText)
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Ручная проверка смотрит последний pending. Несовпадение у другого (старого)
// платежа не должно пугать человека текстом «не прошла проверку» про платёж,
// который он сейчас оплачивает, и не должно само поднимать оповещение.
func TestРучнаяПроверка_НесовпадениеУДругогоПлатежа_НеВлияетНаИтогТекущего(t *testing.T) {
	env := newEdgeEnv(t)
	old := env.pending(t, paymentprovider.YooKassa, 2*time.Hour)
	oldExt := env.externalID(t, old)
	current := env.pending(t, paymentprovider.YooKassa, 5*time.Minute)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		if id == oldExt {
			return http.StatusOK, yooPaymentBody(id, "succeeded", "", 100, "shop", "RUB")
		}
		return http.StatusOK, yooPaymentBody(id, "pending", "", 400, "shop", "RUB")
	}

	msg := checkPaymentByButton(t, env)

	assert.Contains(t, msg, "Оплата пока не поступила")
	assert.NotContains(t, msg, mismatchUserText)
	assert.Equal(t, "pending", env.status(t, current))
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// ---------------------------------------------------------------------------
// Ось отказов: недоступный провайдер и сбой нашей базы
// ---------------------------------------------------------------------------

// Недоступный провайдер на ручной проверке — не несовпадение: прежний текст и
// без оповещения. Иначе сетевой сбой пугал бы человека «не прошла проверку».
func TestРучнаяПроверка_ПровайдерНедоступен_ПрежнийТекстБезОповещения(t *testing.T) {
	cases := map[string]func(env *edgeEnv){
		paymentprovider.YooKassa: func(env *edgeEnv) {
			env.yoo.onGet = func(int, string) (int, string) { return http.StatusInternalServerError, `{}` }
		},
		paymentprovider.Platega: func(env *edgeEnv) {
			env.platega.onGet = func(int, string) (int, string) { return http.StatusBadGateway, `{}` }
		},
	}
	for provider, unavailable := range cases {
		t.Run(provider, func(t *testing.T) {
			env := newEdgeEnv(t)
			unavailable(env)
			id := env.pending(t, provider, 5*time.Minute)

			msg := checkPaymentByButton(t, env)

			assert.Equal(t, "pending", env.status(t, id))
			assert.Contains(t, msg, "Не удалось проверить оплату")
			assert.NotContains(t, msg, mismatchUserText)
			assert.Empty(t, env.tg.matching(mismatchAlertMarker))
		})
	}
}

// Недоступная касса на вебхуке: ошибка, чтобы касса повторила доставку. Ответ
// успехом здесь потерял бы уведомление об оплате навсегда.
func TestВебхук_КассаНедоступна_ОтвечаетОшибкойДляПовтораИНеТревожит(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(int, string) (int, string) { return http.StatusBadGateway, `{}` }
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	err := env.bot.HandleYooKassaWebhook("payment.succeeded", env.externalID(t, id))

	assert.Error(t, err)
	assert.Equal(t, "pending", env.status(t, id))
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// failProviderPaidAtWrites ломает запись момента списания — сбой нашей базы
// внутри проверки ответа, при том что ответ кассы с записью сошёлся.
func failProviderPaidAtWrites(t *testing.T, db *database.DB) {
	t.Helper()
	_, err := db.Conn().Exec(`CREATE TRIGGER fail_paid_at BEFORE UPDATE OF provider_paid_at ON payments
		BEGIN SELECT RAISE(ABORT, 'database is locked'); END`)
	require.NoError(t, err)
}

// Сбой нашей базы на ручной проверке: не несовпадение — человеку не говорим
// «не прошла проверку», владельца не тревожим ложной тревогой о кассе.
func TestРучнаяПроверка_СбойБазыВнутриПроверки_НеВыдаётсяЗаНесовпадение(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	id := env.pending(t, paymentprovider.YooKassa, 5*time.Minute)
	failProviderPaidAtWrites(t, env.db)

	msg := checkPaymentByButton(t, env)

	assert.Equal(t, "pending", env.status(t, id))
	assert.NotContains(t, msg, mismatchUserText)
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Сбой нашей базы на вебхуке: ошибка кассе (повтор доставки может пройти), а
// не молчаливый успех, как у несовпадения, — иначе оплата потерялась бы.
func TestВебхук_СбойБазыВнутриПроверки_ОтвечаетОшибкойАНеУспехом(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	failProviderPaidAtWrites(t, env.db)

	err := env.bot.HandleYooKassaWebhook("payment.succeeded", env.externalID(t, id))

	assert.Error(t, err)
	assert.Equal(t, "pending", env.status(t, id))
	assert.Empty(t, env.tg.matching(mismatchAlertMarker))
}

// Касса подтвердила оплату и ответ СОШЁЛСЯ, но у нас в этот момент не записался
// момент списания. Сверка на сроке считает это молчанием и закрывает платёж
// expired с логом «провайдер не дал ответа» — деньги приняты, подписки нет,
// владелец не знает, а закрытый старше суток платёж больше не сверяется.
func TestСверка_СбойБазыНаСрокеПриОплаченномОтвете_НеЗакрываетОплаченныйПлатёжМолча(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = yooSays("succeeded", "2026-09-27T10:00:00Z")
	id := env.pending(t, paymentprovider.YooKassa, 20*time.Minute)
	failProviderPaidAtWrites(t, env.db)

	env.bot.reconcilePendingPayment(id, env.createdAt(t, id).Add(24*time.Hour), "test")

	closedSilently := env.status(t, id) == "expired" && len(env.tg.all()) == 0
	assert.False(t, closedSilently,
		"оплаченный по сошедшемуся ответу платёж закрыт как неоплаченный, и никто об этом не узнал")
}

// Владелец должен получить оповещение, а не «пометку об оповещении». Если
// Telegram отверг сообщение (429 — точно не доставлено), пометка уже стоит, и
// до перезапуска о деньгах никто не узнает — а человеку на кнопке сказано «мы
// уже разбираемся».
func TestСверка_ОповещениеНеДоставлено_СледующийПроходПовторяетЕго(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 100, "shop", "RUB")
	}
	delivered := failFirstTelegramSend(t, env.bot)
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)

	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")
	env.bot.reconcilePendingPayment(id, time.Now().UTC(), "test")

	assert.Len(t, delivered.matching(mismatchAlertMarker), 1,
		"первое оповещение отвергнуто Telegram — владелец так и не узнал о деньгах")
}

// То же на кнопке: человеку сказали «мы уже разбираемся», а владелец не получил ничего.
func TestРучнаяПроверка_ОповещениеНеДоставлено_ПовторноеНажатиеДоводитЕгоДоВладельца(t *testing.T) {
	env := newEdgeEnv(t)
	env.platega.onGet = plategaSaysAmount(platega.StatusConfirmed, 100)
	delivered := failFirstTelegramSend(t, env.bot)
	env.pending(t, paymentprovider.Platega, 5*time.Minute)

	first := checkPaymentByButton(t, env)
	checkPaymentByButton(t, env)

	require.Contains(t, first, "Мы уже разбираемся")
	assert.Len(t, delivered.matching(mismatchAlertMarker), 1,
		"человеку обещано «мы уже разбираемся», а до владельца не дошло ни одного сообщения")
}

// failFirstTelegramSend — Telegram, который отвергает первое sendMessage с 429
// (сообщение точно не доставлено) и принимает остальные. Возвращает перехват
// только доставленных сообщений.
func failFirstTelegramSend(t *testing.T, b *Bot) *telegramCapture {
	t.Helper()
	capture := &telegramCapture{}
	var calls atomic.Int32
	b.bot = newOfflineTelegramBotForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			return nil, nil
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`))}, nil
		}
		capture.mu.Lock()
		capture.messages = append(capture.messages, parseSentMessage(string(body)))
		capture.mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"ok":true,"result":{"message_id":1,"date":1710000000,"chat":{"id":1,"type":"private"},"text":"ok"}}`))}, nil
	}))
	return capture
}

// ---------------------------------------------------------------------------
// Ось конкуренции: несколько входов на один платёж одновременно
// ---------------------------------------------------------------------------

// Вебхук, сверка и кнопка одновременно видят одно несовпадение: сообщение одно,
// платёж не подтверждён ни одним из них. Заодно это покрывает и последовательный
// случай «вебхук, затем сверка». Гонка здесь дала бы либо спам, либо
// выдачу подписки по несошедшемуся ответу.
func TestГонкаВебхукаСверкиИКнопки_ОдноНесовпадение_ОдноОповещение(t *testing.T) {
	env := newEdgeEnv(t)
	env.yoo.onGet = func(_ int, id string) (int, string) {
		return http.StatusOK, yooPaymentBody(id, "succeeded", "", 400, "other-shop", "RUB")
	}
	id := env.pending(t, paymentprovider.YooKassa, time.Hour)
	ext := env.externalID(t, id)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = env.bot.HandleYooKassaWebhook("payment.succeeded", ext)
		}()
		go func() {
			defer wg.Done()
			env.bot.reconcilePendingPayment(id, time.Now().UTC(), "scheduler")
		}()
		go func() {
			defer wg.Done()
			_, _ = env.bot.checkPaymentStatus(edgeUserID)
		}()
	}
	wg.Wait()

	assert.Equal(t, "pending", env.status(t, id))
	assert.Zero(t, env.panel.patchCount())
	assert.Len(t, env.tg.matching(mismatchAlertMarker), 1)
}

// ---------------------------------------------------------------------------
// Автосписание: несовпадение на своём пути и на общих входах
// ---------------------------------------------------------------------------

// Автосписание уже сообщило о несовпадении своим текстом; вебхук по тому же
// платежу второго сообщения давать не должен.
func TestАвтосписание_НесовпадениеВидитВебхук_ВторогоСообщенияНет(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	body := `{"id":"yo-edge-mm-webhook","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
		  "recipient":{"account_id":"other-shop"}}`
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{body}}
	b, _, capture := setupAutorenewEdgeBot(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-edge-mm-webhook"))
	require.NoError(t, b.HandleYooKassaWebhook("payment.succeeded", "yo-edge-mm-webhook"))

	var mismatchAlerts int
	for _, m := range messagesTo(capture, b.config.AdminID) {
		if strings.Contains(m.Text, "не сошёлся") {
			mismatchAlerts++
		}
	}
	assert.Equal(t, 1, mismatchAlerts)
}

// Несовпавшее автосписание сверка через сутки закрывает expired. Закрытие не
// должно открыть путь второй попытке цикла: деньги по первой ушли, вторая с
// новым ключом списала бы ещё раз.
func TestАвтосписание_НесовпавшийПлатёжЗакрытСверкой_ВторойПопыткиЦиклаНет(t *testing.T) {
	expireAt := time.Now().UTC().Add(6 * time.Hour)
	body := `{"id":"yo-edge-mm-expired","status":"succeeded","amount":{"value":"400.00","currency":"RUB"},
		  "recipient":{"account_id":"other-shop"}}`
	stub := &arEdgeStub{expireAt: expireAt, responses: []string{body}}
	b, db, dbPath, _ := setupAutorenewEdgeBotAt(t, stub)

	b.runAutorenewCharges(time.Now().UTC())
	agePendingPayments(t, dbPath)
	b.reconcilePendingPayments(time.Now().UTC())

	var paymentID int64
	require.NoError(t, db.Conn().QueryRow(`SELECT id FROM payments WHERE provider_payment_id = 'yo-edge-mm-expired'`).Scan(&paymentID))
	p, err := db.GetPaymentByID(paymentID)
	require.NoError(t, err)
	require.Equal(t, "expired", p.Status, "предпосылка: сверка закрыла несовпавший платёж по сроку")

	b.runAutorenewCharges(expireAt.Add(time.Minute))

	charges := 0
	for i := 0; i < stub.callCount(); i++ {
		if stub.call(i).Key != "" {
			charges++
		}
	}
	assert.Equal(t, 1, charges, "по закрытому несовпавшему списанию деньги ушли — второе списание в цикле недопустимо")
}
