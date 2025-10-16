package util

// splitSequences memisahkan byte array menjadi beberapa sequence berdasarkan byte '_' (95)
func splitSequences(buf []byte) [][]byte {
	var sequences [][]byte
	start := 0
	for i, b := range buf {
		if b == 95 { // '_' sebagai separator
			if i > start {
				seq := make([]byte, i-start)
				copy(seq, buf[start:i])
				sequences = append(sequences, seq)
			}
			start = i + 1
		}
	}
	// Tambahkan sisa jika ada
	if start < len(buf) {
		seq := make([]byte, len(buf)-start)
		copy(seq, buf[start:])
		sequences = append(sequences, seq)
	}
	return sequences
}

type AltKeyResult struct {
	IsAltOnly bool   // hanya Alt ditekan tanpa key
	Key       string // "1"-"9" jika ada Alt+angka
}

// Universal parser
func ParseAltUniversal(input []byte) []AltKeyResult {
	results := []AltKeyResult{}
	seqs := splitSequences(input) // pisahkan berdasarkan _ atau byte pattern

	for _, seq := range seqs {
		if len(seq) < 3 {
			continue
		}

		// Lewati prefix ESC+[ atau ESC+O
		start := 0
		if seq[0] == 27 && (seq[1] == '[' || seq[1] == 'O') {
			start = 2
		}

		// Ambil angka-angka di sequence
		nums := []int{}
		num := 0
		hasNum := false
		for i := start; i < len(seq); i++ {
			b := seq[i]
			if b >= '0' && b <= '9' {
				num = num*10 + int(b-'0')
				hasNum = true
			} else if b == ';' || b == '_' {
				if hasNum {
					nums = append(nums, num)
					num = 0
					hasNum = false
				}
			}
		}
		if hasNum {
			nums = append(nums, num)
		}

		// Analisis key
		key := ""
		if len(nums) >= 3 {
			k := nums[2]            // biasanya keyCode di angka ke-3
			if k >= 49 && k <= 57 { // ASCII '1'-'9'
				key = string(rune(k))
			}
		}

		if key != "" {
			results = append(results, AltKeyResult{IsAltOnly: false, Key: key})
		} else {
			results = append(results, AltKeyResult{IsAltOnly: true, Key: ""})
		}
	}

	return results
}
