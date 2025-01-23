package blinkogram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

const (
	apiPathNoteUpsert  = "/api/v1/note/upsert"
	apiPathNoteDetail  = "/api/v1/note/detail"
	apiPathFileUpload  = "/api/file/upload"
	apiPathGetNoteList = "/api/v1/note/list"
	apiPathShareNote   = "/api/v1/note/share"
)

type BlinkoError struct {
	StatusCode int
	Message    string
}

func (e *BlinkoError) Error() string {
	return fmt.Sprintf("blinko error: %d %s", e.StatusCode, e.Message)
}

type FileInfo struct {
	FilePath string      `json:"path"`
	FileName string      `json:"name"`
	Size     interface{} `json:"size"`
	Type     string      `json:"type"`
}

type FileUploadResponse struct {
	FilePath string `json:"filePath"`
	FileName string `json:"fileName"`
	Size     int    `json:"size"`
	Type     string `json:"type"`
}

type BlinkoItem struct {
	ID          int        `json:"id,omitempty"`
	Type        int        `json:"type,omitempty"`
	Content     string     `json:"content"`
	Attachments []FileInfo `json:"attachments,omitempty"`
	IsTop       bool       `json:"isTop"`
	IsShare     bool       `json:"isShare,omitempty"`
}

type BlinkoClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewBlinkoClient(baseURL string) *BlinkoClient {
	return &BlinkoClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *BlinkoClient) UpdateToken(token string) {
	c.token = token
}

func (c *BlinkoClient) HasToken() bool {
	return c.token != ""
}

func (c *BlinkoClient) doRequest(req *http.Request) ([]byte, error) {
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// fmt.Printf("request [%s]: %s\n", req.URL, req.Body)
	// fmt.Printf("response [%s]: %s\n\n", req.URL, string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, &BlinkoError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	return body, nil
}

func (c *BlinkoClient) UpsertBlinko(item BlinkoItem) (BlinkoItem, error) {
	jsonBody, err := json.Marshal(item)
	if err != nil {
		return BlinkoItem{}, err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+apiPathNoteUpsert, bytes.NewBuffer(jsonBody))
	if err != nil {
		return BlinkoItem{}, err
	}

	body, err := c.doRequest(req)
	if err != nil {
		return BlinkoItem{}, err
	}

	var result BlinkoItem
	if err := json.Unmarshal(body, &result); err != nil {
		return BlinkoItem{}, err
	}

	return result, nil
}

func (c *BlinkoClient) UploadFile(fileBytes []byte, filename string) (FileInfo, error) {
	url := c.baseURL + apiPathFileUpload

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return FileInfo{}, err
	}
	part.Write(fileBytes)
	writer.Close()

	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return FileInfo{}, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	res, err := c.doRequest(req)
	if err != nil {
		return FileInfo{}, err
	}

	var tmp FileUploadResponse

	err = json.Unmarshal(res, &tmp)
	if err != nil {
		return FileInfo{}, err
	}

	fileInfo := FileInfo{
		FilePath: tmp.FilePath,
		FileName: tmp.FileName,
		Size:     tmp.Size,
		Type:     tmp.Type,
	}

	return fileInfo, nil
}

func (c *BlinkoClient) GetNoteDetail(id int) (BlinkoItem, error) {
	url := c.baseURL + apiPathNoteDetail

	body := map[string]interface{}{
		"id": id,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return BlinkoItem{}, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return BlinkoItem{}, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return BlinkoItem{}, err
	}

	var blinkoItem BlinkoItem
	if err := json.Unmarshal(resp, &blinkoItem); err != nil {
		return BlinkoItem{}, err
	}

	return blinkoItem, nil
}

func (c *BlinkoClient) GetNoteList(searchText string) ([]BlinkoItem, error) {
	url := c.baseURL + apiPathGetNoteList

	body := map[string]interface{}{
		"searchText": searchText,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}

	var blinkoItems []BlinkoItem
	err = json.Unmarshal(resp, &blinkoItems)
	if err != nil {
		return nil, err
	}

	return blinkoItems, nil
}

func (c *BlinkoClient) ShareNote(memoID int, isShare bool) error {
	url := c.baseURL + apiPathShareNote

	body := map[string]interface{}{
		"id":       memoID,
		"isCancel": !isShare,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}

	_, err = c.doRequest(req)
	if err != nil {
		return err
	}

	return nil
}
