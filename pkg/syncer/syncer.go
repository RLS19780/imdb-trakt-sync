package syncer

import (
	"context"
	"errors"
	"github.com/cecobask/imdb-trakt-sync/pkg/client"
	"github.com/cecobask/imdb-trakt-sync/pkg/entities"
	"github.com/cecobask/imdb-trakt-sync/pkg/logger"
	"go.uber.org/zap"
	"os"
	"strings"
)

const (
	EnvVarKeyCookieAtMain   = "IMDB_COOKIE_AT_MAIN"
	EnvVarKeyCookieUbidMain = "IMDB_COOKIE_UBID_MAIN"
	EnvVarKeyListIds        = "IMDB_LIST_IDS"
	EnvVarKeyUserId         = "IMDB_USER_ID"
	EnvVarKeyClientId       = "TRAKT_CLIENT_ID"
	EnvVarKeyClientSecret   = "TRAKT_CLIENT_SECRET"
	EnvVarKeyPassword       = "TRAKT_PASSWORD"
	EnvVarKeyUsername       = "TRAKT_USERNAME"
)

type Syncer struct {
	logger      *zap.Logger
	imdbClient  client.ImdbClientInterface
	traktClient client.TraktClientInterface
	user        *user
}

type user struct {
	lists   []entities.DataPair
	ratings entities.DataPair
}

func NewSyncer() *Syncer {
	syncer := &Syncer{
		logger: logger.NewLogger(),
		imdbClient: client.NewImdbClient(client.ImdbConfig{
			CookieAtMain:   os.Getenv(EnvVarKeyCookieAtMain),
			CookieUbidMain: os.Getenv(EnvVarKeyCookieUbidMain),
			UserId:         os.Getenv(EnvVarKeyUserId),
		}),
		traktClient: client.NewTraktClient(client.TraktConfig{
			ClientId:     os.Getenv(EnvVarKeyClientId),
			ClientSecret: os.Getenv(EnvVarKeyClientSecret),
			Username:     os.Getenv(EnvVarKeyUsername),
			Password:     os.Getenv(EnvVarKeyPassword),
		}),
		user: &user{},
	}
	if imdbListIdsString := os.Getenv(EnvVarKeyListIds); imdbListIdsString != "" && imdbListIdsString != "all" {
		imdbListIds := strings.Split(imdbListIdsString, ",")
		for i := range imdbListIds {
			syncer.user.lists = append(syncer.user.lists, entities.DataPair{
				ImdbListId: imdbListIds[i],
			})
		}
	}
	return syncer
}

func (s *Syncer) Run() {
	_ = context.Background()
	if err := validateEnvVars(); err != nil {
		s.logger.Fatal("failed to validate environment variables", zap.Error(err))
	}
	s.hydrate()
	s.syncLists()
	s.syncRatings()
}

func validateEnvVars() error {
	requiredEnvVarKeys := []string{
		EnvVarKeyListIds,
		EnvVarKeyCookieAtMain,
		EnvVarKeyCookieUbidMain,
		EnvVarKeyClientId,
		EnvVarKeyClientSecret,
		EnvVarKeyUsername,
		EnvVarKeyPassword,
	}
	var missingEnvVars []string
	for i := range requiredEnvVarKeys {
		if _, ok := os.LookupEnv(requiredEnvVarKeys[i]); !ok {
			missingEnvVars = append(missingEnvVars, requiredEnvVarKeys[i])
		}
	}
	if len(missingEnvVars) > 0 {
		return NewMissingEnvironmentVariablesError(missingEnvVars)
	}
	return nil
}

func (s *Syncer) hydrate() {
	if len(s.user.lists) != 0 {
		s.user.lists = s.cleanupLists()
	} else {
		s.user.lists = s.imdbClient.ListsScrape()
	}
	watchlistId, imdbList, _ := s.imdbClient.WatchlistGet()
	s.user.lists = append(s.user.lists, entities.DataPair{
		ImdbList:     imdbList,
		ImdbListId:   *watchlistId,
		ImdbListName: "watchlist",
		IsWatchlist:  true,
	})
	for i := range s.user.lists {
		list := &s.user.lists[i]
		if list.IsWatchlist {
			list.TraktList = s.traktClient.WatchlistItemsGet()
			continue
		}
		traktList, err := s.traktClient.ListItemsGet(list.TraktListId)
		if errors.Is(err, client.ErrNotFound) {
			s.traktClient.ListAdd(list.TraktListId, list.ImdbListName)
		}
		list.TraktList = traktList
	}
	s.user.ratings = entities.DataPair{
		ImdbList:  s.imdbClient.RatingsGet(),
		TraktList: s.traktClient.RatingsGet(),
	}
}

func (s *Syncer) syncLists() {
	for _, list := range s.user.lists {
		diff := list.Difference()
		if list.IsWatchlist {
			if len(diff["add"]) > 0 {
				s.traktClient.WatchlistItemsAdd(diff["add"])
			}
			if len(diff["remove"]) > 0 {
				s.traktClient.WatchlistItemsRemove(diff["remove"])
			}
			continue
		}
		if len(diff["add"]) > 0 {
			s.traktClient.ListItemsAdd(list.TraktListId, diff["add"])
		}
		if len(diff["remove"]) > 0 {
			s.traktClient.ListItemsRemove(list.TraktListId, diff["remove"])
		}
	}
	// Remove lists that only exist in Trakt
	traktLists := s.traktClient.ListsGet()
	for _, tl := range traktLists {
		if !contains(s.user.lists, tl.Name) {
			s.traktClient.ListRemove(tl.Ids.Slug)
		}
	}
}

func (s *Syncer) syncRatings() {
	diff := s.user.ratings.Difference()
	if len(diff["add"]) > 0 {
		s.traktClient.RatingsAdd(diff["add"])
		for _, ti := range diff["add"] {
			history := s.traktClient.HistoryGet(ti)
			if len(history) > 0 {
				continue
			}
			s.traktClient.HistoryAdd([]entities.TraktItem{ti})
		}
	}
	if len(diff["remove"]) > 0 {
		s.traktClient.RatingsRemove(diff["remove"])
		for _, ti := range diff["remove"] {
			history := s.traktClient.HistoryGet(ti)
			if len(history) == 0 {
				continue
			}
			s.traktClient.HistoryRemove([]entities.TraktItem{ti})
		}
	}
	var ratingsToUpdate []entities.TraktItem
	for _, imdbItem := range s.user.ratings.ImdbList {
		if imdbItem.Rating != nil {
			for _, traktItem := range s.user.ratings.TraktList {
				switch traktItem.Type {
				case entities.TraktItemTypeMovie:
					if imdbItem.Id == traktItem.Movie.Ids.Imdb && *imdbItem.Rating != traktItem.Rating {
						traktItem.Movie.Rating = *imdbItem.Rating
						traktItem.Movie.RatedAt = imdbItem.RatingDate.UTC().String()
						ratingsToUpdate = append(ratingsToUpdate, traktItem)
					}
				case entities.TraktItemTypeShow:
					if imdbItem.Id == traktItem.Show.Ids.Imdb && *imdbItem.Rating != traktItem.Rating {
						traktItem.Show.Rating = *imdbItem.Rating
						traktItem.Show.RatedAt = imdbItem.RatingDate.UTC().String()
						ratingsToUpdate = append(ratingsToUpdate, traktItem)
					}
				case entities.TraktItemTypeEpisode:
					if imdbItem.Id == traktItem.Episode.Ids.Imdb && *imdbItem.Rating != traktItem.Rating {
						traktItem.Episode.Rating = *imdbItem.Rating
						traktItem.Episode.RatedAt = imdbItem.RatingDate.UTC().String()
						ratingsToUpdate = append(ratingsToUpdate, traktItem)
					}
				}
			}
		}
	}
	if len(ratingsToUpdate) > 0 {
		s.traktClient.RatingsAdd(ratingsToUpdate)
	}
}

// cleanupLists removes invalid imdb lists passed via the IMDB_LIST_IDS
// env variable and returns only the lists that actually exist
func (s *Syncer) cleanupLists() []entities.DataPair {
	lists := make([]entities.DataPair, len(s.user.lists))
	n := 0
	for i := range s.user.lists {
		imdbListName, imdbList, err := s.imdbClient.ListItemsGet(s.user.lists[i].ImdbListId)
		if errors.Is(err, errors.New("resource not found")) {
			continue
		}
		lists[n] = entities.DataPair{
			ImdbList:     imdbList,
			ImdbListId:   s.user.lists[i].ImdbListId,
			ImdbListName: *imdbListName,
			TraktListId:  client.FormatTraktListName(*imdbListName),
		}
		n++
	}
	return lists[:n]
}

func contains(dps []entities.DataPair, traktListName string) bool {
	for _, dp := range dps {
		if dp.ImdbListName == traktListName {
			return true
		}
	}
	return false
}
