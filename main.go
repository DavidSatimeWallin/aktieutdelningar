package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty"
	"github.com/gocolly/colly"
)

const (
	currencyAPIURL = "http://free.currencyconverterapi.com/api/v5/convert?q=%s_SEK&compact=y"
	baseURL        = "https://www.avanza.se"
	dividendsPath  = "/placera/foretagskalendern/utdelningar.html"
)

var (
	dateRegex = regexp.MustCompile(`(?P<date>[0-9]{4}-[0-9]{2}-[0-9]{2})`)
	divRegex  = regexp.MustCompile(`(?P<sum>[0-9]+,[0-9]+) (?P<curr>[A-Z]+)`)

	defaultCurrencyMap = map[string]float64{
		"NOK_SEK": 1.07517,
		"EUR_SEK": 10.256293,
		"DKK_SEK": 1.376561,
	}

	// Which currencies do we want to look up?
	currencies = []string{
		"NOK",
		"DKK",
		"EUR",
	}
)

type (
	stockData struct {
		name        string
		score       float64
		price       float64
		dividend    float64
		sekdividend float64
		currency    string
		exchange    float64
		sekprice    float64
	}
)

type byScore []stockData

func (v byScore) Len() int           { return len(v) }
func (v byScore) Swap(i, j int)      { v[i], v[j] = v[j], v[i] }
func (v byScore) Less(i, j int) bool { return v[i].score > v[j].score }

var (
	StockMap map[string]stockData
	PriceMap map[string]float64
)

func main() {
	c := colly.NewCollector()
	StockMap = make(map[string]stockData)
	PriceMap = make(map[string]float64)
	now := time.Now()
	today := now.Format("2006-01-02")
	compactToday := strings.Replace(today, "-", "", -1)
	currstamp, err := strconv.Atoi(compactToday)
	if err != nil {
		fmt.Println("error converting compactToday to int", err.Error())
	}

	currencyMap := generateCurrencyMap(currencies)

	c.OnHTML(".quoteBar", func(e *colly.HTMLElement) {
		name := e.Attr("data-intrument_name")
		buyPriceString := e.ChildText(".buyPrice")
		if buyPriceString != "-" && !strings.Contains(buyPriceString, "\\u") {
			buyPrice, err := strconv.ParseFloat(strings.Replace(buyPriceString, ",", ".", -1), 64)
			if err != nil {
				fmt.Println("error parsing buyPriceString to float", err.Error())
			}
			PriceMap[name] = buyPrice
		}
	})

	c.OnHTML(".companyCalendarList", func(e *colly.HTMLElement) {
		e.ForEach(".companyCalendarItem", func(_ int, el *colly.HTMLElement) {
			stock := stockData{
				name: el.ChildText(".azaLink"),
			}
			el.ForEach("ul.companyCalendarItemList li", func(_ int, ul *colly.HTMLElement) {

				// Pick the last day to buy to get dividend
				if strings.Contains(ul.Text, "Handlas utan utdelning") {
					match := dateRegex.FindStringSubmatch(ul.Text)
					result := make(map[string]string)
					if len(match) < 1 {
						return
					}
					for i, name := range dateRegex.SubexpNames() {
						if i != 0 && name != "" {
							result[name] = match[i]
						}
					}
					compactDate := strings.Replace(result["date"], "-", "", -1)
					buydate, err := strconv.Atoi(compactDate)
					if err != nil {
						fmt.Println("error converting compactDate to int", err.Error())
					}
					if buydate > currstamp {
						StockMap[stock.name] = stock
					}
				}

				// Pick the dividend of the stock
				if strings.Contains(ul.Text, "Ordinarie utdelning") {
					test := strings.TrimSpace(strings.Replace(ul.Text, "Ordinarie utdelning:\n", "", -1))
					match := divRegex.FindStringSubmatch(test)
					result := make(map[string]string)
					for i, name := range divRegex.SubexpNames() {
						if i != 0 && name != "" {
							result[name] = match[i]
						}
					}
					sumString := strings.Replace(result["sum"], ",", ".", -1)
					sum, err := strconv.ParseFloat(sumString, 64)
					if err != nil {
						fmt.Println("error converting sum string to float64", err.Error())
					}
					sumFloat, err := strconv.ParseFloat(strings.Replace(sumString, ".", "", -1), 64)
					if err != nil {
						fmt.Println("error converting sumString to int", err.Error())
					}
					stock.score = stock.score + sumFloat
					stock.dividend = sum
					stock.currency = result["curr"]
					switch stock.currency {
					case "SEK":
						stock.exchange = 1.0
					default:
						stock.exchange = currencyMap[fmt.Sprintf("%s_SEK", stock.currency)]
					}
				}
			})
			link := baseURL + el.ChildAttr("a", "href")
			c.Visit(link)
		})
	})

	c.Visit(baseURL + dividendsPath)

	buildResults()

}

func buildResults() {
	stocks := []stockData{}
	for _, v := range StockMap {
		if v.dividend < 1 {
			continue
		}
		stock := stockData{
			name:        v.name,
			price:       PriceMap[v.name],
			dividend:    v.dividend,
			sekdividend: v.dividend * v.exchange,
			currency:    v.currency,
			exchange:    v.exchange,
			sekprice:    PriceMap[v.name] * v.exchange,
		}
		stock.score = v.score + stock.sekprice
		stocks = append(stocks, stock)
	}
	sort.Sort(byScore(stocks))
	c := 0
outputLoop:
	for _, v := range stocks {
		if c > 4 {
			break outputLoop
		}
		fmt.Printf("Namn:\t\t\t\t %s \nKostar:\t\t\t\t %f.2 \nUtdelning:\t\t\t %f.2 \nAtt investera per utdelad SEK\t %f.2\n\n", v.name, v.sekprice, v.sekdividend, (v.sekprice / v.sekdividend))
		c++
	}
}

func generateCurrencyMap(currencies []string) (currencyMap map[string]float64) {
	currencyMap = make(map[string]float64)
	for _, v := range currencies {
		url := fmt.Sprintf(currencyAPIURL, v)
		resp, err := resty.R().Get(url)
		if err != nil {
			fmt.Println("error making resty request to", url, err.Error())
		}
		if resp.StatusCode() != 200 {
			currencyMap = defaultCurrencyMap
			return
		}
		t := make(map[string]map[string]float64)
		respBody := []byte(resp.String())
		if err := json.Unmarshal(respBody, &t); err != nil {
			fmt.Println("error parsing json into map", err.Error())
		}
		for k, v := range t {
			currencyMap[k] = v["val"]
		}
	}
	return
}
