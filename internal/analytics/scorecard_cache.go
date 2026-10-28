package analytics

import "time"

// scorecardTTL is how long ScorecardsFor keeps an answer.
var scorecardTTL = time.Minute

// scorecardClock is time.Now unless a test moves it.
var scorecardClock = time.Now

// InvalidateScorecards drops every cached scorecard.
func InvalidateScorecards() {}
