package client

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/cecobask/imdb-trakt-sync/pkg/entities"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	imdbCookieNameAtMain   = "at-main"
	imdbCookieNameUbidMain = "ubid-main"

	imdbHeaderKeyContentDisposition = "Content-Disposition"

	imdbPathBase          = "https://www.imdb.com"
	imdbPathListExport    = "/list/%s/export"
	imdbPathLists         = "/user/%s/lists"
	imdbPathProfile       = "/profile"
	imdbPathRatingsExport = "/user/%s/ratings/export"
	imdbPathWatchlist     = "/watchlist"

	imdbResponseTypeList    = "list"
	imdbResponseTypeRatings = "ratings"
)

type ImdbClient struct {
	endpoint string
	client   *http.Client
	Config   ImdbConfig
}

type ImdbConfig struct {
	CookieAtMain   string
	CookieUbidMain string
	UserId         string
	WatchlistId    string
}

func NewImdbClient(config ImdbConfig) ImdbClientInterface {
	client := &ImdbClient{
		endpoint: imdbPathBase,
		client:   &http.Client{},
		Config:   config,
	}
	client.Hydrate()
	return client
}

func (c *ImdbClient) Hydrate() {
	if c.Config.UserId == "" || c.Config.UserId == "scrape" {
		c.Config.UserId = c.UserIdScrape()
	}
	c.Config.WatchlistId = c.WatchlistIdScrape()
}

func (c *ImdbClient) doRequest(params requestParams) *http.Response {
	req, err := http.NewRequest(params.Method, c.endpoint+params.Path, nil)
	if err != nil {
		log.Fatalf("error creating http request %s, %s: %v", params.Method, c.endpoint+params.Path, err)
	}
	req.AddCookie(&http.Cookie{
		Name:  imdbCookieNameAtMain,
		Value: c.Config.CookieAtMain,
	})
	req.AddCookie(&http.Cookie{
		Name:  imdbCookieNameUbidMain,
		Value: c.Config.CookieUbidMain,
	})
	if params.Body != nil {
		body, err := json.Marshal(params.Body)
		if err != nil {
			log.Fatalf("error marshalling request body %s, %s: %v", params.Method, c.endpoint+params.Path, err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	res, err := c.client.Do(req)
	if err != nil {
		log.Fatalf("error sending http request %s, %s: %v", params.Method, c.endpoint+params.Path, err)
	}
	return res
}

func (c *ImdbClient) ListItemsGet(listId string) (*string, []entities.ImdbItem, error) {
	res := c.doRequest(requestParams{
		Method: http.MethodGet,
		Path:   fmt.Sprintf(imdbPathListExport, listId),
	})
	defer DrainBody(res.Body)
	switch res.StatusCode {
	case http.StatusOK:
		break
	case http.StatusForbidden:
		log.Fatalf("error retrieving imdb list %s for user %s: update the imdb cookie values", listId, c.Config.UserId)
	case http.StatusNotFound:
		log.Printf("error retrieving imdb list %s for user %s: %v", listId, c.Config.UserId, res.StatusCode)
		return nil, nil, errors.New("resource not found")
	default:
		log.Fatalf("error retrieving imdb list %s for user %s: %v", listId, c.Config.UserId, res.StatusCode)
	}
	listName, list := readResponse(res, imdbResponseTypeList)
	return listName, list, nil
}

func (c *ImdbClient) WatchlistGet() (*string, []entities.ImdbItem, error) {
	res := c.doRequest(requestParams{
		Method: http.MethodGet,
		Path:   fmt.Sprintf(imdbPathListExport, c.Config.WatchlistId),
	})
	defer DrainBody(res.Body)
	switch res.StatusCode {
	case http.StatusOK:
		break
	case http.StatusForbidden:
		log.Fatalf("error retrieving imdb list %s for user %s: update the imdb cookie values", c.Config.WatchlistId, c.Config.UserId)
	case http.StatusNotFound:
		log.Printf("error retrieving imdb list %s for user %s: %v", c.Config.WatchlistId, c.Config.UserId, res.StatusCode)
		return nil, nil, errors.New("resource not found")
	default:
		log.Fatalf("error retrieving imdb list %s for user %s: %v", c.Config.WatchlistId, c.Config.UserId, res.StatusCode)
	}
	_, list := readResponse(res, imdbResponseTypeList)
	return &c.Config.WatchlistId, list, nil
}

func (c *ImdbClient) ListsScrape() (dp []entities.DataPair) {
	res := c.doRequest(requestParams{
		Method: http.MethodGet,
		Path:   fmt.Sprintf(imdbPathLists, c.Config.UserId),
	})
	defer DrainBody(res.Body)
	switch res.StatusCode {
	case http.StatusOK:
		break
	case http.StatusForbidden:
		log.Fatalf("error scraping imdb lists for user %s: update the imdb cookie values", c.Config.UserId)
	default:
		log.Fatalf("error scraping imdb lists for user %s: %v", c.Config.UserId, res.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatalf("error creating goquery document from imdb response: %v", err)
	}
	doc.Find(".user-list").Each(func(i int, selection *goquery.Selection) {
		imdbListId, ok := selection.Attr("id")
		if !ok {
			log.Fatalf("error scraping imdb lists for user %s: none found", c.Config.UserId)
		}
		imdbListName, imdbList, err := c.ListItemsGet(imdbListId)
		if errors.Is(err, errors.New("")) {
			return
		}
		dp = append(dp, entities.DataPair{
			ImdbList:     imdbList,
			ImdbListId:   imdbListId,
			ImdbListName: *imdbListName,
			TraktListId:  FormatTraktListName(*imdbListName),
		})
	})
	return dp
}

func (c *ImdbClient) UserIdScrape() string {
	res := c.doRequest(requestParams{
		Method: http.MethodGet,
		Path:   imdbPathProfile,
	})
	defer DrainBody(res.Body)
	switch res.StatusCode {
	case http.StatusOK:
		break
	case http.StatusForbidden:
		log.Fatalf("error scraping imdb profile: update the imdb cookie values")
	default:
		log.Fatalf("error scraping imdb profile: %v", res.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatalf("error creating goquery document from imdb response: %v", err)
	}
	userId, ok := doc.Find(".user-profile.userId").Attr("data-userid")
	if !ok {
		log.Fatalf("error scraping imdb profile: user id not found")
	}
	return userId
}

func (c *ImdbClient) WatchlistIdScrape() string {
	res := c.doRequest(requestParams{
		Method: http.MethodGet,
		Path:   imdbPathWatchlist,
	})
	defer DrainBody(res.Body)
	switch res.StatusCode {
	case http.StatusOK:
		break
	case http.StatusForbidden:
		log.Fatalf("error scraping imdb watchlist id: update the imdb cookie values")
	default:
		log.Fatalf("error scraping imdb watchlist id: %v", res.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatalf("error creating goquery document from imdb response: %v", err)
	}
	watchlistId, ok := doc.Find("meta[property='pageId']").Attr("content")
	if !ok {
		log.Fatalf("error scraping imdb watchlist id: watchlist id not found")
	}
	return watchlistId
}

func (c *ImdbClient) RatingsGet() []entities.ImdbItem {
	res := c.doRequest(requestParams{
		Method: http.MethodGet,
		Path:   fmt.Sprintf(imdbPathRatingsExport, c.Config.UserId),
	})
	defer DrainBody(res.Body)
	switch res.StatusCode {
	case http.StatusOK:
		break
	case http.StatusForbidden:
		log.Fatalf("error retrieving imdb ratings for user %s: update the imdb cookie values", c.Config.UserId)
	case http.StatusNotFound:
		log.Printf("error retrieving imdb ratings for user %s: none found", c.Config.UserId)
		return nil
	default:
		log.Fatalf("error retrieving imdb ratings for user %s: %v", c.Config.UserId, res.StatusCode)
	}
	_, ratings := readResponse(res, imdbResponseTypeRatings)
	return ratings
}

func readResponse(res *http.Response, resType string) (imdbListName *string, imdbList []entities.ImdbItem) {
	csvReader := csv.NewReader(res.Body)
	csvReader.LazyQuotes = true
	csvReader.FieldsPerRecord = -1
	csvData, err := csvReader.ReadAll()
	if err != nil {
		log.Fatalf("error reading imdb response: %v", err)
	}
	switch resType {
	case imdbResponseTypeList:
		for i, record := range csvData {
			if i > 0 { // omit header line
				imdbList = append(imdbList, entities.ImdbItem{
					Id:        record[1],
					TitleType: record[7],
				})
			}
		}
		contentDispositionHeader := res.Header.Get(imdbHeaderKeyContentDisposition)
		if contentDispositionHeader == "" {
			log.Fatalf("error reading header %s from imdb response", imdbHeaderKeyContentDisposition)
		}
		_, params, err := mime.ParseMediaType(contentDispositionHeader)
		if err != nil || len(params) == 0 {
			log.Fatalf("error parsing media type from header: %v", err)
		}
		imdbListName = &strings.Split(params["filename"], ".")[0]
	case imdbResponseTypeRatings:
		for i, record := range csvData {
			if i > 0 {
				rating, err := strconv.Atoi(record[1])
				if err != nil {
					log.Fatalf("error parsing imdb rating value: %v", err)
				}
				ratingDate, err := time.Parse("2006-01-02", record[2])
				if err != nil {
					log.Fatalf("error parsing imdb rating date: %v", err)
				}
				imdbList = append(imdbList, entities.ImdbItem{
					Id:         record[0],
					TitleType:  record[5],
					Rating:     &rating,
					RatingDate: &ratingDate,
				})
			}
		}
	default:
		log.Fatalf("unknown imdb response type")
	}
	return imdbListName, imdbList
}

func FormatTraktListName(imdbListName string) string {
	formatted := strings.ToLower(strings.Join(strings.Fields(imdbListName), "-"))
	re := regexp.MustCompile(`[^-a-z0-9]+`)
	return re.ReplaceAllString(formatted, "")
}

func DrainBody(body io.ReadCloser) {
	err := body.Close()
	if err != nil {
		log.Fatalf("error closing response body: %v", err)
	}
}
