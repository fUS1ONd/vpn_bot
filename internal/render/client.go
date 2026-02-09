package render

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// Client — HTTP-клиент для render-сервиса
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient создаёт новый клиент render API
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 5 * time.Minute, // загрузка файлов может быть долгой
		},
	}
}

// TaskResponse — ответ при создании/получении задачи
type TaskResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CreateVideoTask создаёт задачу рендеринга видео из аудио + аватарки.
// Отправляет multipart: mode=video, audio_file, avatar_file, username.
func (c *Client) CreateVideoTask(audio io.Reader, avatar io.Reader, username string) (*TaskResponse, error) {
	// Используем io.Pipe чтобы не буферизовать весь файл в памяти
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Формируем multipart-тело в горутине
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Поле mode
		if err := writer.WriteField("mode", "video"); err != nil {
			errCh <- fmt.Errorf("не удалось записать поле mode: %w", err)
			return
		}

		// Поле username
		if err := writer.WriteField("username", username); err != nil {
			errCh <- fmt.Errorf("не удалось записать поле username: %w", err)
			return
		}

		// Файл audio_file
		audioPart, err := writer.CreateFormFile("audio_file", "audio.ogg")
		if err != nil {
			errCh <- fmt.Errorf("не удалось создать часть audio_file: %w", err)
			return
		}
		if _, err := io.Copy(audioPart, audio); err != nil {
			errCh <- fmt.Errorf("не удалось скопировать audio_file: %w", err)
			return
		}

		// Файл avatar_file
		avatarPart, err := writer.CreateFormFile("avatar_file", "avatar.jpg")
		if err != nil {
			errCh <- fmt.Errorf("не удалось создать часть avatar_file: %w", err)
			return
		}
		if _, err := io.Copy(avatarPart, avatar); err != nil {
			errCh <- fmt.Errorf("не удалось скопировать avatar_file: %w", err)
			return
		}

		errCh <- nil
	}()

	// Отправляем запрос параллельно с записью тела
	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/tasks", pr)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать запрос: %w", err)
	}

	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос не удался: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем ошибку из горутины записи
	if writeErr := <-errCh; writeErr != nil {
		return nil, writeErr
	}

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API ошибка %d: %s", resp.StatusCode, string(body))
	}

	var result TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("не удалось декодировать ответ: %w", err)
	}

	return &result, nil
}

// CreateCircleTask создаёт задачу рендеринга субтитров на кружок.
// Отправляет multipart: mode=circle, video_file, username.
func (c *Client) CreateCircleTask(video io.Reader, username string) (*TaskResponse, error) {
	// Используем io.Pipe чтобы не буферизовать весь файл в памяти
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Формируем multipart-тело в горутине
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Поле mode
		if err := writer.WriteField("mode", "circle"); err != nil {
			errCh <- fmt.Errorf("не удалось записать поле mode: %w", err)
			return
		}

		// Поле username
		if err := writer.WriteField("username", username); err != nil {
			errCh <- fmt.Errorf("не удалось записать поле username: %w", err)
			return
		}

		// Файл video_file
		videoPart, err := writer.CreateFormFile("video_file", "video.mp4")
		if err != nil {
			errCh <- fmt.Errorf("не удалось создать часть video_file: %w", err)
			return
		}
		if _, err := io.Copy(videoPart, video); err != nil {
			errCh <- fmt.Errorf("не удалось скопировать video_file: %w", err)
			return
		}

		errCh <- nil
	}()

	// Отправляем запрос параллельно с записью тела
	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/tasks", pr)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать запрос: %w", err)
	}

	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос не удался: %w", err)
	}
	defer resp.Body.Close()

	// Проверяем ошибку из горутины записи
	if writeErr := <-errCh; writeErr != nil {
		return nil, writeErr
	}

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API ошибка %d: %s", resp.StatusCode, string(body))
	}

	var result TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("не удалось декодировать ответ: %w", err)
	}

	return &result, nil
}

// GetTaskStatus получает статус задачи по её ID
func (c *Client) GetTaskStatus(taskID string) (*TaskResponse, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/v1/tasks/"+taskID, nil)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать запрос: %w", err)
	}

	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос не удался: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API ошибка %d: %s", resp.StatusCode, string(body))
	}

	var result TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("не удалось декодировать ответ: %w", err)
	}

	return &result, nil
}

// DownloadResult скачивает результат задачи (MP4-файл).
// Вызывающая сторона обязана закрыть возвращённый io.ReadCloser.
func (c *Client) DownloadResult(taskID string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/v1/tasks/"+taskID+"/result", nil)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать запрос: %w", err)
	}

	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос не удался: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API ошибка %d: %s", resp.StatusCode, string(body))
	}

	// Возвращаем тело ответа напрямую — вызывающая сторона закрывает
	return resp.Body, nil
}
