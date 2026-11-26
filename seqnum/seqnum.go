// Package seqnum 原子编码生成器（对应 PHP CodeRule）。
//
// 使用 Redis INCR 保证原子性 + DB MAX 恢复（首次或 Redis 丢数据时），
// 支持 36/10/26 进制编码 + 首字符范围校验（算法按位转换）。
package seqnum

import (
	"context"
	"fmt"
	"time"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/redislock"

	"github.com/gogf/gf/v2/frame/g"
)

// CodeRule 编码规则（对应 PHP CodeRuleInterface）
type CodeRule struct {
	Name       string
	Prefix     string
	Suffix     string
	Length     int
	Padding    string
	CharType   int // 36=BOTH, 10=NUMBER, 26=LETTER
	TableName  string
	ColumnName string
	FcMin      int
	FcMax      int
}

const (
	CharTypeBoth   = 36
	CharTypeNumber = 10
	CharTypeLetter = 26
)

var (
	dictBoth   = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" // 36
	dictNumber = "0123456789"                           // 10
	dictLetter = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"           // 26
)

// 恢复路径 DB 读取注入点：生产为 recoverFromDB（g.DB() 直连）；
// 同包单测替换为伪实现以覆盖并发恢复安全（设计 D2b），对外不可见
var recoverFromDBFn = recoverFromDB

// 恢复互斥参数：锁 TTL 覆盖一次 DB MAX 查询 + 回写远绰绰有余；
// 未抢到锁者按此间隔自旋重试，累计超过 recoverWaitMax 返回明确错误
const (
	recoverLockTTL    = 10
	recoverRetryGap   = 50 * time.Millisecond
	recoverWaitMax    = 5 * time.Second
	recoverLockPrefix = "seqnum-recover:"
)

// NextCode 生成下一个编码（Redis INCR 原子操作 + DB 恢复）。
//
// 恢复安全（Redis 丢数据防重号）：INCR 结果为 1 视为计数丢失，进入互斥恢复——
// 抢到恢复锁者以「DB 最大码 + 原子提升」重建计数并取码；未抢到锁者等待后重入。
// 正确性不变量：任何发出的码都 > DB 恢复基准（floor 缓存只由恢复锁内写入）。
// 恢复完成后常规路径读 floor 缓存快速判定，不再进锁。
func NextCode(ctx context.Context, rule CodeRule) (string, error) {
	redisKey := fmt.Sprintf("seq_num:%s", rule.Name)

	deadline := time.Now().Add(recoverWaitMax)
	for {
		seq, err := g.Redis().Incr(ctx, redisKey)
		if err != nil {
			return "", fmt.Errorf("Redis INCR 失败: %w", err)
		}
		seqInt := int(seq)

		// floor==-1（基准未建立：冷启动/丢数据）时不放行，必须先走恢复建立基准
		if floor := recoverFloor(ctx, rule, redisKey); floor > 0 && seqInt > floor {
			return buildCode(seqInt, rule)
		}

		// floor 未建立（冷启动/丢数据）或 seq 未越过基准：进入互斥恢复
		recovered, err := recoverLocked(ctx, rule, redisKey)
		if err != nil {
			return "", err
		}
		if recovered > 0 {
			return buildCode(recovered, rule)
		}
		// 未抢到锁：等待持有者完成恢复后重入
		if time.Now().After(deadline) {
			return "", exception.New(exception.CodeSeqNumGenFailed,
				fmt.Sprintf("序列号恢复等待超时（>%s）rule=%s", recoverWaitMax, rule.Name))
		}
		time.Sleep(recoverRetryGap)
	}
}

// recoverFloor 返回计数「有效」的下界（seq > floor 才可作为新码发出）。
// floor 缓存 key（seq_num:<name>:floor）只由恢复流程在恢复锁内写入（SETNX），
// 本函数绝不写入——并发半途读到的计数现值不可靠，据其缓存会把 floor 压低/错判。
// 无缓存时返回 -1：调用方必须走恢复流程建立 floor 后再判定。
func recoverFloor(ctx context.Context, rule CodeRule, redisKey string) int {
	floorKey := redisKey + ":floor"
	floor, err := g.Redis().Get(ctx, floorKey)
	if err == nil && floor.Int() > 0 {
		return floor.Int()
	}
	return -1
}

// recoverLocked 互斥执行 DB 恢复（唯一写 floor 缓存的地方）。
// 返回 >0 为本调用取得的码序号（> DB 最大序号）；返回 0 表示未抢到锁（调用方应等待重试）
func recoverLocked(ctx context.Context, rule CodeRule, redisKey string) (int, error) {
	lock := redislock.Create(recoverLockPrefix+rule.Name, recoverLockTTL)
	if !lock.Get(ctx) {
		return 0, nil
	}
	defer lock.Release(ctx)

	dbSeq := recoverFromDBFn(ctx, rule)
	if dbSeq < 1 {
		dbSeq = 1
	}

	// 原子提升：仅当现值 < dbSeq 时才写 dbSeq（绝不覆盖更大的现值——
	// 他人可能已 INCR 到恢复后区间），随后 INCR 取得越过基准的下一个
	raiseScript := `local c = tonumber(redis.call("GET", KEYS[1]) or "0")
if c < tonumber(ARGV[1]) then redis.call("SET", KEYS[1], ARGV[1]) end
return redis.call("INCR", KEYS[1])`
	result, err := g.Redis().Eval(ctx, raiseScript, 1, []string{redisKey}, []any{dbSeq})
	if err != nil {
		return 0, fmt.Errorf("Redis 恢复提升脚本失败: %w", err)
	}

	// 建立恢复基准缓存（后续调用快速判定；只在此处写）
	floorKey := redisKey + ":floor"
	g.Redis().Do(ctx, "SET", floorKey, dbSeq, "NX")
	// 若并发恢复已有更高基准（理论上 DB 只读不变，防御式），以现存值为准
	if fl, e := g.Redis().Get(ctx, floorKey); e == nil && fl.Int() > dbSeq {
		dbSeq = fl.Int()
	}

	seq := result.Int()
	if seq <= dbSeq {
		// 提升脚本返回值未越过基准（被并发写入压低），再原子推进一步
		seq2, err := g.Redis().Eval(ctx, raiseScript, 1, []string{redisKey}, []any{dbSeq})
		if err != nil {
			return 0, fmt.Errorf("Redis 恢复提升脚本失败: %w", err)
		}
		seq = seq2.Int()
	}
	return seq, nil
}

// GetCurrentCode 读取当前编码（不递增计数）：返回当前计数对应的完整编码；
// 尚无计数（首次 NextCode 前）返回空串。连续调用返回相同值。
func GetCurrentCode(ctx context.Context, rule CodeRule) (string, error) {
	redisKey := fmt.Sprintf("seq_num:%s", rule.Name)
	cur, err := g.Redis().Get(ctx, redisKey)
	if err != nil {
		return "", fmt.Errorf("Redis GET 失败: %w", err)
	}
	seq := cur.Int()
	if seq <= 0 {
		return "", nil
	}
	return buildCode(seq, rule)
}

// buildCode 序号 → 完整编码（进制转换 + 首字符校验 + 前后缀/填充拼装）
func buildCode(seqInt int, rule CodeRule) (string, error) {
	code := intToCode(seqInt, rule.CharType)

	if err := validateFirstChar(code, rule); err != nil {
		return "", err
	}

	for len(code) < rule.Length {
		code = rule.Padding + code
	}

	return rule.Prefix + code + rule.Suffix, nil
}

func intToCode(num int, charType int) string {
	dict, base := getDict(charType)
	if num == 0 {
		return string(dict[0])
	}
	result := ""
	for num > 0 {
		result = string(dict[num%base]) + result
		num = num / base
	}
	return result
}

func codeToInt(code string, charType int) int {
	dict, base := getDict(charType)
	result := 0
	for _, ch := range code {
		idx := indexOf(dict, ch)
		if idx < 0 {
			return 0
		}
		result = result*base + idx
	}
	return result
}

func recoverFromDB(ctx context.Context, rule CodeRule) int {
	record, err := g.DB().Model(rule.TableName).Ctx(ctx).
		Fields(rule.ColumnName).
		Order(rule.ColumnName + " DESC").
		One()
	if err != nil || record == nil || record.IsEmpty() {
		return 1
	}
	maxCode := record[rule.ColumnName].String()
	prefixLen := len(rule.Prefix)
	suffixLen := len(rule.Suffix)
	if len(maxCode) > prefixLen+suffixLen {
		seqPart := maxCode[prefixLen : len(maxCode)-suffixLen]
		seq := codeToInt(seqPart, rule.CharType)
		return seq + 1
	}
	return 1
}

func getDict(charType int) (string, int) {
	switch charType {
	case CharTypeNumber:
		return dictNumber, 10
	case CharTypeLetter:
		return dictLetter, 26
	default:
		return dictBoth, 36
	}
}

func validateFirstChar(code string, rule CodeRule) error {
	if rule.FcMin == 0 && rule.FcMax == 0 {
		return nil
	}
	if len(code) == 0 {
		return fmt.Errorf("编码为空，无法校验首字符")
	}
	dict, _ := getDict(rule.CharType)
	firstChar := rune(code[0])
	idx := indexOf(dict, firstChar)
	if idx < 0 {
		return fmt.Errorf("首字符 %c 不在字典中", firstChar)
	}
	if idx < rule.FcMin || idx > rule.FcMax {
		return fmt.Errorf("首字符 %c 索引 %d 超出允许范围 [%d, %d]", firstChar, idx, rule.FcMin, rule.FcMax)
	}
	return nil
}

func indexOf(s string, ch rune) int {
	for i, c := range s {
		if c == ch {
			return i
		}
	}
	return -1
}
