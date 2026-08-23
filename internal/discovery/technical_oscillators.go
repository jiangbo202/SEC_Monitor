package discovery

import "math"

const (
	standardKDJMethod   = "ohlc_9_3_3"
	closeRangeKDJMethod = "close_range_9_3_3"
)

type closeOscillatorPoint struct {
	RSI14     *float64
	K         *float64
	D         *float64
	J         *float64
	KDJMethod string
}

func calculateCloseOscillators(rows []PriceSnapshot) []closeOscillatorPoint {
	result := make([]closeOscillatorPoint, len(rows))
	if len(rows) == 0 {
		return result
	}
	closes := make([]float64, len(rows))
	for index, row := range rows {
		closes[index] = priceSnapshotClose(row)
	}

	if len(closes) > technicalRSIPeriod {
		gainTotal, lossTotal := 0.0, 0.0
		for index := 1; index <= technicalRSIPeriod; index++ {
			change := closes[index] - closes[index-1]
			if change > 0 {
				gainTotal += change
			} else {
				lossTotal -= change
			}
		}
		averageGain := gainTotal / technicalRSIPeriod
		averageLoss := lossTotal / technicalRSIPeriod
		value := wilderRSI(averageGain, averageLoss)
		result[technicalRSIPeriod].RSI14 = float64Pointer(value)
		for index := technicalRSIPeriod + 1; index < len(closes); index++ {
			change := closes[index] - closes[index-1]
			gain, loss := 0.0, 0.0
			if change > 0 {
				gain = change
			} else {
				loss = -change
			}
			averageGain = (averageGain*float64(technicalRSIPeriod-1) + gain) / technicalRSIPeriod
			averageLoss = (averageLoss*float64(technicalRSIPeriod-1) + loss) / technicalRSIPeriod
			result[index].RSI14 = float64Pointer(wilderRSI(averageGain, averageLoss))
		}
	}

	kValue, dValue := 50.0, 50.0
	for index := technicalKDJPeriod - 1; index < len(closes); index++ {
		windowRows := rows[index-technicalKDJPeriod+1 : index+1]
		windowCloses := closes[index-technicalKDJPeriod+1 : index+1]
		method := standardKDJMethod
		for _, row := range windowRows {
			if !priceSnapshotHasOHLC(row) {
				method = closeRangeKDJMethod
				break
			}
		}
		low, high := windowCloses[0], windowCloses[0]
		if method == standardKDJMethod {
			low, high = priceSnapshotLow(windowRows[0]), priceSnapshotHigh(windowRows[0])
			for _, row := range windowRows[1:] {
				low = math.Min(low, priceSnapshotLow(row))
				high = math.Max(high, priceSnapshotHigh(row))
			}
		} else {
			for _, closeValue := range windowCloses[1:] {
				low = math.Min(low, closeValue)
				high = math.Max(high, closeValue)
			}
		}
		rsv := 50.0
		if high > low {
			rsv = (closes[index] - low) / (high - low) * 100
		}
		kValue = kValue*2/3 + rsv/3
		dValue = dValue*2/3 + kValue/3
		jValue := 3*kValue - 2*dValue
		result[index].K = float64Pointer(kValue)
		result[index].D = float64Pointer(dValue)
		result[index].J = float64Pointer(jValue)
		result[index].KDJMethod = method
	}
	return result
}

func buildCandidateOscillatorAnalysis(rows []PriceSnapshot) CandidateOscillatorAnalysis {
	analysis := CandidateOscillatorAnalysis{
		Status:    TechnicalStatusMissing,
		KDJMethod: closeRangeKDJMethod,
		Signal:    "unavailable",
		Label:     "历史不足",
		Reasons:   []string{},
	}
	points := calculateCloseOscillators(rows)
	if len(points) == 0 {
		return analysis
	}
	latest := points[len(points)-1]
	analysis.RSI14, analysis.K, analysis.D, analysis.J = latest.RSI14, latest.K, latest.D, latest.J
	if latest.KDJMethod != "" {
		analysis.KDJMethod = latest.KDJMethod
	}
	if latest.RSI14 == nil || latest.K == nil || latest.D == nil || latest.J == nil {
		analysis.Status = TechnicalStatusDataInsufficient
		return analysis
	}
	analysis.Status = TechnicalStatusReady
	analysis.Signal = "neutral"
	analysis.Label = "动能中性"

	var previous closeOscillatorPoint
	if len(points) >= 2 {
		previous = points[len(points)-2]
	}
	if previous.RSI14 != nil && *previous.RSI14 <= 30 && *latest.RSI14 > 30 {
		analysis.Signal, analysis.Label = "bullish", "RSI 超卖修复"
		analysis.Reasons = append(analysis.Reasons, "RSI(14) 自 30 下方回升")
	} else if previous.RSI14 != nil && *previous.RSI14 >= 70 && *latest.RSI14 < 70 {
		analysis.Signal, analysis.Label = "bearish", "RSI 高位转弱"
		analysis.Reasons = append(analysis.Reasons, "RSI(14) 自 70 上方回落")
	} else if *latest.RSI14 >= 70 {
		analysis.Signal, analysis.Label = "caution", "RSI 偏热"
		analysis.Reasons = append(analysis.Reasons, "RSI(14) 位于 70 以上，关注短线过热")
	} else if *latest.RSI14 <= 30 {
		analysis.Signal, analysis.Label = "watch", "RSI 超卖观察"
		analysis.Reasons = append(analysis.Reasons, "RSI(14) 位于 30 以下，尚需等待修复确认")
	}

	if previous.K != nil && previous.D != nil {
		kdjLabel := "KDJ"
		if analysis.KDJMethod == closeRangeKDJMethod {
			kdjLabel = "收盘价近似 KDJ"
		}
		goldenCross := *previous.K <= *previous.D && *latest.K > *latest.D
		deathCross := *previous.K >= *previous.D && *latest.K < *latest.D
		if goldenCross && *latest.D < 80 {
			analysis.Signal, analysis.Label = "bullish", "KDJ 金叉"
			analysis.Reasons = append(analysis.Reasons, kdjLabel+" 的 K 线上穿 D 线")
		} else if deathCross && *latest.D > 20 {
			analysis.Signal, analysis.Label = "bearish", "KDJ 死叉"
			analysis.Reasons = append(analysis.Reasons, kdjLabel+" 的 K 线下穿 D 线")
		}
	}
	if len(analysis.Reasons) == 0 {
		switch {
		case *latest.RSI14 >= 60 && *latest.K > *latest.D:
			analysis.Signal, analysis.Label = "bullish", "动能偏强"
			analysis.Reasons = append(analysis.Reasons, "RSI(14) 位于强势区且 K 高于 D")
		case *latest.RSI14 <= 40 && *latest.K < *latest.D:
			analysis.Signal, analysis.Label = "bearish", "动能偏弱"
			analysis.Reasons = append(analysis.Reasons, "RSI(14) 位于弱势区且 K 低于 D")
		default:
			analysis.Reasons = append(analysis.Reasons, "RSI 与 KDJ 暂未形成一致方向")
		}
	}
	return analysis
}

func priceSnapshotHasOHLC(row PriceSnapshot) bool {
	return row.OpenMicros > 0 && row.HighMicros > 0 && row.LowMicros > 0 && row.CloseMicros > 0 &&
		row.HighMicros >= row.OpenMicros && row.HighMicros >= row.CloseMicros &&
		row.LowMicros <= row.OpenMicros && row.LowMicros <= row.CloseMicros && row.HighMicros >= row.LowMicros
}

func priceSnapshotOpen(row PriceSnapshot) float64 { return float64(row.OpenMicros) / 1_000_000 }
func priceSnapshotHigh(row PriceSnapshot) float64 { return float64(row.HighMicros) / 1_000_000 }
func priceSnapshotLow(row PriceSnapshot) float64  { return float64(row.LowMicros) / 1_000_000 }

func wilderRSI(averageGain, averageLoss float64) float64 {
	if averageGain == 0 && averageLoss == 0 {
		return 50
	}
	if averageLoss == 0 {
		return 100
	}
	if averageGain == 0 {
		return 0
	}
	return 100 - 100/(1+averageGain/averageLoss)
}

func float64Pointer(value float64) *float64 {
	rounded := math.Round(value*100) / 100
	return &rounded
}
