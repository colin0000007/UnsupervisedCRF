package UnsupervisedCRF

// 	非监督学习CRF实现

type UnsupervisedCRF struct {
	t        int
	stateNum int
}

func (p *UnsupervisedCRF) GetDefaultTemplate() string {
	return `
				# Unigram
	            U0:%x[-1,0]
	            U1:%x[0,0]
	            U2:%x[1,0]
	            U3:%x[-2,0]%x[-1,0]
	            U4:%x[-1,0]%x[0,0]
	            U5:%x[0,0]%x[1,0]
	            U6:%x[1,0]%x[2,0]
	            # Bigram
	            B
		
		`
}

// 前向算法
func (p *UnsupervisedCRF) forward(x []int, mMatrix [][][]float64) {
	var alpha = make([][]float64, p.t, p.stateNum)
	for i := 0; i < p.stateNum; i++ {
		alpha[0][i] = mMatrix[0][0][i]
	}
}
