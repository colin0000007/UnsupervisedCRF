package UnsupervisedCRF

import (
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"sort"
)

/*
算法何其复杂
DAT中的有限状态自动机（DFA）：
1.前置说明：
   code：
   	字符编码
   trie：
   	code构成的前缀树
   状态：
   	存在一个offset，令其为状态s，它是一个数组索引的偏移量
2.状态转移base：
   	给定一个单词"c[1]c[2]c[3]...c[n]",c[i]表示单词中第i个字符的编码
   	转移方程为：
   		base[s_[i] + c[i]] = s_[i+1]
   		i = 0,s_[0] = 1，表示根节点的状态
		注意:s_[i+1]仅仅表示输入单词中c[i]转移到的特征，而不对应有限状态集合Q中的Q[i+1]
   		有以下：
   			s_[0] = 1
   			base[0 + c[1]] = s_[1]
   			base[s_[1]+c[2]] = s_[3]
   			...
   		这个base就是用于存储状态转移的数组，它接受输入s_[i]和c[i]，输出下一个状态s_[i+1]
3.check数组：
   	对于互为兄弟节点的c1，c2，c3，c4....cn （意味着它们有共同的前缀）
   	若在check数组中，有check[s+c1] = check[s+c2] =check[s+c3] =...=0(为0表示该处位置未被占用)
       那么令check[s+c1] = check[s+c2] = check[s+c3] =...=s
   	check相当于保存了c1，c2，c3...共同的前缀状态s
3.无论是check，还是base，它们都接受一个状态s1和一个code，从而产生一个新的状态s2，产生使用了索引，这也是高效的原因
*/

// 构建trie树使用的节点
type node struct {
	//字符编码,这里的编码是unicode + 1，+1是略过root节点，
	// 若不+1，unicode = 0的字符将占用base[0],check[0]
	code  int
	depth int //所处树的层级，正好对应子节点在key中索引
	left  int // 当前字符在key list中搜索的左边界索引 （包括）
	right int // 当前字符在key list中搜索的右边界索引（不包括）
}

func (n *node) String() string {
	d := fmt.Sprint(n.depth)
	l := fmt.Sprint(n.left)
	r := fmt.Sprint(n.right)
	return "['code': " + string(rune(n.code-1)) + ",'d': " + d + ",'l': " + l + ",'r': " + r + "]"
}

const (
	BUF_SIZE  = 16384
	UNIT_SIZE = 8
	// 初始化base大小
	INIT_SIZE = 65536 * 32
)

type key []rune
type DoubleArrayTrie struct {
	check        []int
	base         []int
	size         int         //对于base，check真正用到的大小
	allocSize    int         // 分配的数组大小
	keys         []key       // key list, []rune对应单个key
	keySize      int         //key的数量
	values       interface{} //k-v中的v
	progress     int         // 构建进度
	nextCheckPos int         //下一次insert可能开始的检查位置
}

type DATExport struct {
	Check        []int
	Base         []int
	Size         int
	AllocSize    int
	Keys         []key
	KeySize      int
	Values       interface{}
	Progress     int
	NextCheckPos int
}

func NewDoubleArrayTrie() *DoubleArrayTrie {
	return &DoubleArrayTrie{}
}

/*
	对base，used，check扩容
*/
func (d *DoubleArrayTrie) resize(newSize int) int {
	base2 := make([]int, newSize, newSize)
	check2 := make([]int, newSize, newSize)
	if d.allocSize > 0 {
		copy(base2, d.base)
		copy(check2, d.check)
	}
	d.base = base2
	d.check = check2
	d.allocSize = newSize
	return newSize
}

func (d *DoubleArrayTrie) GetKeySize() int {
	return d.keySize
}

/*
对于输入的词语：
a
ab
abc
dd
dgh
hello
列的方向来看，同一列的字符处于trie树的同一层
找到parent的孩子节点，并界定它们的孩子节点搜索范围
eg:
对于
ab
abc
be
bfg
c
若当前parent = 'root'
那么结果[]*node将产生：
[['code': a,'d': 1,'l': 0,'r': 2] ['code': b,'d': 1,'l': 2,'r': 4] ['code': c,'d': 1,'l': 4,'r': 5]]
注：d:depth,l:left,r:right

@param:[]*node 孩子节点
*/
func (d *DoubleArrayTrie) fetch(parent *node) ([]*node, error) {
	// 搜索范围left->right
	// 搜索层：parent.depth
	var pre rune
	children := make([]*node, 0)
	for i := parent.left; i < parent.right; i++ {
		keyLen := len(d.keys[i])
		//"a,ab",parent='a'这种情况需要跳过 'a'
		if keyLen == parent.depth {
			continue
		}
		cur := d.keys[i][parent.depth] + 1
		// ab,abc,abe,abf，aef,parent = 'a',扫描到连续的b时略过
		if cur == pre {
			continue
		}
		// 非字典序
		if pre > cur {
			msg := fmt.Sprintf("keys are not dict order, pre=%c, cur=%c,key=%s", pre-1, cur-1, string(d.keys[i]))
			return children, errors.New(msg)
		}
		newNode := new(node)
		newNode.left = i
		newNode.depth = parent.depth + 1
		newNode.code = int(cur)
		// 扫描到和上一个字符不重复，更新上一个字符的右边界
		if len(children) > 0 {
			children[len(children)-1].right = i
		}
		children = append(children, newNode)
		pre = cur
	}
	if len(children) > 0 {
		children[len(children)-1].right = parent.right
	}
	//log.Printf("%v", children)
	return children, nil
}

/*
 核心：
	对于输入的children，
	（1）若children为空，退出
	（2）否则，找到一个合适的offset，使得check[offset+c1] = check[offset+c2] = ...=0
	（3）DFS的接续构建树，依次对子节点构建c1，c2，c3，调用fetch，调用insert回到（1）
@param int：返回一个当前找到的offset：
*/
func (d *DoubleArrayTrie) insert(children []*node) (int, error) {
	pos := 0
	// 	启发式方法可以避免每次begin从0开始检测
	if d.nextCheckPos > children[0].code {
		pos = d.nextCheckPos - 1
	} else {
		pos = children[0].code
	}
	begin := 0 // 偏移量,>=1,base[0] = 1，第一个begin必定为1
	firstNonZero := true
	nonZeroNum := 0 // 非0的pos计数
outer:
	for {
		pos++
		begin = pos - children[0].code
		// 被占用
		if d.check[pos] != 0 {
			nonZeroNum++
			continue
		} else if firstNonZero {
			d.nextCheckPos = pos
			firstNonZero = false
		}
		// 扩容
		if s := begin + children[len(children)-1].code + 1; s >= d.allocSize {
			rate := 0.0
			pr := float64(1.0 * d.keySize / (d.progress + 1))
			if 1.05 > pr {
				rate = 1.05
			} else {
				rate = pr
			}
			d.resize(int(float64(s) * rate))
		}
		for i := 1; i < len(children); i++ {
			if d.check[begin+children[i].code] != 0 {
				// 之前这里写的continue导致了bug
				continue outer
			}
		}
		break
	}
	//fmt.Println("find begin:", begin)
	if s := begin + children[len(children)-1].code + 1; d.size < s {
		d.size = s
	}
	// 简单的启发式方法：如果检查过的位置中95%都是被占用的（nextCheckPos（第一次开始有check=0）～pos（成功找到begin的一次的pos））
	// 那么设置nextCheckPos为检查结束的值，可能避免下一次insert从begin=0开始检测
	if s := pos - d.nextCheckPos + 1; float64(nonZeroNum*1.0/s) >= 0.95 {
		d.nextCheckPos = pos
	}
	for i := 0; i < len(children); i++ {
		d.check[begin+children[i].code] = begin
	}
	// 针对孩子节点继续递归构建
	for _, chi := range children {
		nodes, err := d.fetch(chi)
		if err != nil {
			log.Fatal(err)
			return begin, err
		}
		// 没有孩子节点
		if len(nodes) == 0 {
			// -1 是为了确保base值小于0
			// !!! 这个赋值非常重要
			// 当一个key是独立存在的，非前缀，拿到的base值可以还原为left，此left就是该key的的index
			// 通过状态转移后，拿到的base值可以得到index
			d.base[begin+chi.code] = -chi.left - 1
			// 到叶子节点用掉一个key（不包括公共前缀）
			d.progress++
		} else {
			nexState, err := d.insert(nodes)
			if err != nil {
				log.Fatal(err)
				return begin, err
			}
			// 状态转移
			d.base[begin+chi.code] = nexState
		}
	}
	return begin, nil
}

// 只用keys来build
func (d *DoubleArrayTrie) Build1(keys []string) error {
	return d.build_(keys, nil, false)
}

// 用key和value来build
func (d *DoubleArrayTrie) Build2(keys []string, vals interface{}) error {
	return d.build_(keys, vals, false)
}

// 用key来build，并且对key排序
func (d *DoubleArrayTrie) BuildWithSort(keys []string) error {
	return d.build_(keys, nil, true)
}

// BOBgrk&600
func (d *DoubleArrayTrie) build_(keys []string, vals interface{}, needSort bool) error {
	if vals != nil {
		typeOf := reflect.TypeOf(vals)
		if typeOf.Kind() != reflect.Slice {
			log.Fatalln("vals are not slice type")
			return errors.New("vals are not slice type")
		}
		d.values = vals
	}
	if needSort {
		sort.Strings(keys)
	}
	return d.build(keys)
}

// 底层构建方法
func (d *DoubleArrayTrie) build(keys []string) error {
	if keys == nil || len(keys) == 0 {
		log.Fatal("empty keys")
		return errors.New("empty keys")
	}
	keys2 := make([]key, 0, len(keys))
	for _, str := range keys {
		keys2 = append(keys2, []rune(str))
	}
	d.keys = keys2
	d.keySize = len(keys)
	// base，check默认初始大小
	d.resize(INIT_SIZE)
	d.nextCheckPos = 0
	root := new(node)
	root.left = 0
	root.right = d.keySize
	root.depth = 0
	children, err := d.fetch(root)
	if err != nil {
		log.Fatal(err)
		return err
	}
	begin, err := d.insert(children)
	if err != nil {
		log.Fatal(err)
		return err
	}
	log.Println("first begin = ", begin)
	d.base[0] = begin // 应该为1
	log.Println("build done...")
	log.Println("DAT:", d)
	// 压缩数组
	d.Shrink()
	return nil
}

/*
 压缩base，check数组
 长度压缩为真实的size
*/
func (d *DoubleArrayTrie) Shrink() {
	d.resize(d.size)
}

/*
	根据key返回value
*/
func (d *DoubleArrayTrie) GetValue(key string) interface{} {
	if d.values == nil {
		return nil
	}
	index, ok := d.IndexOf(key)
	if ok {
		valueOf := reflect.ValueOf(d.values)
		// 有问题，返回的是一个slice
		return valueOf.Index(index).Interface()
	}
	return nil
}

//返回一个key在数组的索引
//@return int:返回一个key的索引
//@return bool: 是否出错
func (d *DoubleArrayTrie) IndexOf(key string) (int, bool) {
	return d.ExactMatchSearch(key)
}

//在DAT中搜索给定key，
//搜索成功：返回key所在的index
//为什么能拿到key的index？
//	这个问题我刚开始想了很久也没明白
//	当到达叶子节点时，它的base值赋值为了-left-1，这个left实际上就是当前key的index，
//	所以当我搜索时能够直接拿到index值
//@return int: 索引值
//		-1：出现key错误或者转移过程不满足check
//		-2：当前key作为了公共前缀，无法得到index
//		>=0: 当前key正常搜索到
// TODO 至今还不清楚为什么原代码中需要在for外面多转一次
func (d *DoubleArrayTrie) ExactMatchSearch(key string) (res int, ok bool) {
	if len(key) == 0 {
		return -1, false
	}
	chs := []rune(key)
	kLen := len(chs)
	begin := d.base[0]
	// root + code(a) -> s1, check[root + code[a]] = root
	// s1 + code[b] -> s2, check[s1 + code[b]] = s1
	for i := 0; i < kLen; i++ {
		// 状态转移函数的输入
		index := begin + int(chs[i]+1)
		if d.check[index] != begin {
			log.Fatalf("error transition, begin = %v, check[index]=%v,code=%c\n", begin, d.check[index], chs[i])
			return -1, false
		}
		// 转移到下一个状态
		begin = d.base[index]
	}
	//log.Println("begin:", begin)
	if begin < 0 {
		return -begin - 1, true
	}
	return -2, false
}

func (d *DoubleArrayTrie) String() string {
	size := fmt.Sprint(d.size)
	alSize := fmt.Sprint(d.allocSize)
	return `
	[
		size : ` + size + `,
		allocSize : ` + alSize + `,
		keySize: ` + fmt.Sprint(d.keySize) + `,
		progress: ` + fmt.Sprint(d.progress) + `,	
	]
           `
}

/*
TODO
*/
func (d *DoubleArrayTrie) CommonPrefixSearch(prefix string) []string {
	subs := make([]string, 1)
	return subs
}

// 保存build好的DAT到指定路径
func (d *DoubleArrayTrie) Store(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		log.Println(err)
		return err
	}
	defer file.Close()
	encoder := gob.NewEncoder(file)
	dat := new(DATExport)

	dat.AllocSize = d.allocSize
	dat.Base = d.base
	dat.Check = d.check
	dat.Keys = d.keys
	dat.NextCheckPos = d.nextCheckPos
	dat.Progress = d.progress
	dat.Size = d.size
	dat.Values = d.values
	dat.KeySize = d.keySize

	err = encoder.Encode(dat)
	if err != nil {
		log.Println(err)
		return err
	}
	return nil
}

// 从指定路径加载DAT
func (d *DoubleArrayTrie) Load(path string) error {
	file, err := os.Open(path)
	if err != nil {
		log.Println(file)
		return err
	}
	defer file.Close()
	decoder := gob.NewDecoder(file)
	dat := new(DATExport)
	err = decoder.Decode(dat)
	if err != nil {
		log.Println(err)
		return err
	}

	d.allocSize = dat.AllocSize
	d.base = dat.Base
	d.check = dat.Check
	d.keys = dat.Keys
	d.nextCheckPos = dat.NextCheckPos
	d.progress = dat.Progress
	d.size = dat.Size
	d.values = dat.Values
	d.keySize = dat.KeySize

	return nil
}

/**
一举
一举一动
一举成名
一举成名天下知
万能
万能胶
字典词条：6
胶    名    动    知    下    成    举    一    能    天    万
33014 21517 21160 30693 19979 25104 20030 19968 33021 22825 19975
是否错误: 0
DoubleArrayTrie{
char =     ×    一    万    ×    举    ×    动    ×    下    名    ×    知    ×    ×    能    一    天    成    胶
i    =      0 19970 19977 20032 20033 21162 21164 21519 21520 21522 30695 30699 33023 33024 33028 40001 44345 45137 66038
base =      1     2     6    -1 20032    -2 21162    -3     5 21519    -4 30695    -5    -6 33023     3  1540     4 33024
check=      0     1     1 20032     2 21162     3 21519  1540     4 30695     5 33023 33024     6 20032 21519 20032 33023
size=66039, allocSize=2097152, key=null, keySize=6, progress=6, nextCheckPos=33024, error_=0}

i       =      0     0     1     1     2     2     3     3     4     4     5     5     6     6    循环退出
b       =      1     1     2     2 20032 20032     4     4 21519 21519  1540  1540     5     5    30695

p       =      1 19970     2 20033 20032 45137     4 21522 21519 44345  1540 21520     5 30699    30695
base[p] =      1     2     0 20032    -1     4     0 21519    -3  1540     0     5     0 30695    -4
check[p]=      0     1     0     2 20032 20032     0     4 21519 21519     0  1540     0     5    30695
一举
一举成名
一举成名天下知
*/
