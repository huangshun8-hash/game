package logic

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/idx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/gmtdto"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// MailLogic 邮件业务逻辑
type MailLogic struct {
	mongoSvc *mongox.Service
	idGen    *idx.Snowflake
	// lazyCreateGroup 按 playerID 去重并发的 Lazy Create，防止同玩家并发下发导致邮箱溢出
	lazyCreateGroup singleflight.Group
}

// NewMailLogic 创建邮件业务逻辑
func NewMailLogic(mongoSvc *mongox.Service) *MailLogic {
	return &MailLogic{mongoSvc: mongoSvc, idGen: idx.Default()}
}

func (l *MailLogic) repos() (*repo.MailTemplateRepo, *repo.PlayerMailRepo) {
	client := l.mongoSvc.Client()
	return repo.NewMailTemplateRepo(client), repo.NewPlayerMailRepo(client)
}

// SendMail GMT 运营发送邮件，创建系统邮件模板（采用 Lazy Create 模式按需下发）
// req: GMT 发送邮件请求（标题/正文/附件/目标/过期时间等）
// requestID: 请求追踪ID，由 handler 从请求上下文读取后显式传入
// 返回：邮件模板ID与创建时间
func (l *MailLogic) SendMail(ctx context.Context, req *gmtdto.GmtSendMailReq, requestID string) (*gmtdto.GmtSendMailResp, error) {
	templateRepo, _ := l.repos()

	rewards := make([]model.Reward, 0, len(req.Rewards))
	for _, r := range req.Rewards {
		rewards = append(rewards, model.Reward{ItemID: r.ItemID, Count: r.Count})
	}

	now := time.Now().UnixMilli()
	template := &model.MailTemplate{
		TemplateID: l.idGen.NextID(),
		Title: model.TextContent{
			Type: int32(req.TitleType),
			Text: req.Title,
		},
		Content: model.TextContent{
			Type: int32(req.ContentType),
			Text: req.Content,
		},
		Sender:     req.Sender,
		Rewards:    rewards,
		TargetType: req.TargetType,
		TargetUIDs: req.TargetUIDs,
		CreateTime: now,
		StartTime:  req.StartTime,
		EndTime:    req.EndTime,
		ExpireTime: req.ExpireTime,
	}
	if err := templateRepo.InsertTemplate(ctx, template); err != nil {
		logx.Error("GMT 发送邮件模板创建失败",
			zap.String("requestId", requestID),
			zap.Int64("templateId", template.TemplateID),
			zap.String("operatorId", req.OperatorID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("创建邮件模板失败: %w", err)
	}

	logx.Info("GMT 发送邮件模板创建成功",
		zap.String("requestId", requestID),
		zap.Int64("templateId", template.TemplateID),
		zap.String("operatorId", req.OperatorID),
		zap.String("title", req.Title),
		zap.Int("targetType", req.TargetType),
		zap.Int("targetCount", len(req.TargetUIDs)),
		zap.Int("rewardCount", len(rewards)),
	)
	return &gmtdto.GmtSendMailResp{
		TemplateID: strconv.FormatInt(template.TemplateID, 10),
		CreateTime: template.CreateTime,
	}, nil
}

// ListTemplates GMT 查询邮件模板列表（分页）
// req: 查询请求（模板ID/目标类型/撤回状态/创建时间范围过滤 + 分页参数）
// requestID: 请求追踪ID，由 handler 从请求上下文读取后显式传入
// 返回：当前页模板列表与分页元信息
func (l *MailLogic) ListTemplates(ctx context.Context, req *gmtdto.GmtListTemplatesReq, requestID string) (*gmtdto.GmtListTemplatesResp, error) {
	templateRepo, _ := l.repos()

	// 将字符串 TemplateID 解析为 int64 用于数据库过滤
	var filterTemplateID int64
	if req.TemplateID != "" && req.TemplateID != "0" {
		parsed, err := strconv.ParseInt(req.TemplateID, 10, 64)
		if err == nil && parsed > 0 {
			filterTemplateID = parsed
		}
	}

	pageResult, err := templateRepo.PageTemplates(ctx, repo.MailTemplateFilter{
		TemplateID: filterTemplateID,
		TargetType: req.TargetType,
		Recalled:   req.Recalled,
		StartTime:  req.StartTime,
		EndTime:    req.EndTime,
	}, req.Page, req.PageSize)
	if err != nil {
		logx.Error("GMT 查询邮件模板列表失败",
			zap.String("requestId", requestID),
			zap.String("templateId", req.TemplateID),
			zap.Any("targetType", req.TargetType),
			zap.Any("recalled", req.Recalled),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询邮件模板列表失败: %w", err)
	}

	items := make([]*gmtdto.GmtMailTemplateItem, 0, len(pageResult.Items))
	for i := range pageResult.Items {
		t := &pageResult.Items[i]
		items = append(items, &gmtdto.GmtMailTemplateItem{
			TemplateID:  strconv.FormatInt(t.TemplateID, 10),
			Title:       t.Title.Text,
			TitleType:   int(t.Title.Type),
			Content:     t.Content.Text,
			ContentType: int(t.Content.Type),
			Sender:      t.Sender,
			Rewards:     rewardsToGmt(t.Rewards),
			TargetType:  t.TargetType,
			TargetUIDs:  t.TargetUIDs,
			CreateTime:  t.CreateTime,
			StartTime:   t.StartTime,
			EndTime:     t.EndTime,
			ExpireTime:  t.ExpireTime,
			IsRecalled:  t.IsRecalled,
			RecallTime:  t.RecallTime,
		})
	}

	return &gmtdto.GmtListTemplatesResp{
		Items:      items,
		Total:      pageResult.Total,
		Page:       pageResult.Page,
		PageSize:   pageResult.PageSize,
		TotalPages: pageResult.TotalPages,
	}, nil
}

// RecallTemplate GMT 撤回邮件模板
// 撤回语义：仅标记模板为已撤回，不再向未收到邮件的玩家下发（Lazy Create 过滤已撤回模板）；
// 已下发到玩家邮箱的邮件不受影响，仍可正常查看/领取。
// req: 撤回请求（模板业务ID）
// requestID: 请求追踪ID，由 handler 从请求上下文读取后显式传入
// 返回：模板ID、当前撤回状态与撤回时间（幂等：已撤回的模板重复撤回视为成功）
func (l *MailLogic) RecallTemplate(ctx context.Context, req *gmtdto.GmtRecallMailReq, requestID string) (*gmtdto.GmtRecallMailResp, error) {
	templateRepo, _ := l.repos()

	templateID, err := strconv.ParseInt(req.TemplateID, 10, 64)
	if err != nil || templateID <= 0 {
		return nil, model.ErrMailTemplateNotFound
	}

	template, err := templateRepo.FindByTemplateID(ctx, templateID)
	if err != nil {
		if errors.Is(err, mongox.ErrNotFound) {
			return nil, model.ErrMailTemplateNotFound
		}
		logx.Error("GMT 查询邮件模板失败",
			zap.String("requestId", requestID),
			zap.Int64("templateId", templateID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询邮件模板失败: %w", err)
	}

	// 已撤回的模板重复撤回视为成功（幂等），返回既有撤回时间
	if template.IsRecalled {
		logx.Info("GMT 撤回邮件模板（已处于撤回状态）",
			zap.String("requestId", requestID),
			zap.Int64("templateId", templateID),
			zap.String("operatorId", req.OperatorID),
			zap.Int64("recallTime", template.RecallTime),
		)
		return &gmtdto.GmtRecallMailResp{
			TemplateID: req.TemplateID,
			IsRecalled: true,
			RecallTime: template.RecallTime,
		}, nil
	}

	nowMs := time.Now().UnixMilli()
	modified, err := templateRepo.RecallTemplate(ctx, templateID, nowMs)
	if err != nil {
		logx.Error("GMT 撤回邮件模板失败",
			zap.String("requestId", requestID),
			zap.Int64("templateId", templateID),
			zap.String("operatorId", req.OperatorID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("撤回邮件模板失败: %w", err)
	}

	// CAS 未命中说明并发下已被其他请求撤回，视为成功
	if !modified {
		logx.Info("GMT 撤回邮件模板（并发下已被撤回）",
			zap.String("requestId", requestID),
			zap.Int64("templateId", templateID),
			zap.String("operatorId", req.OperatorID),
		)
	}

	logx.Info("GMT 撤回邮件模板成功",
		zap.String("requestId", requestID),
		zap.Int64("templateId", templateID),
		zap.String("operatorId", req.OperatorID),
		zap.Int64("recallTime", nowMs),
	)
	return &gmtdto.GmtRecallMailResp{
		TemplateID: req.TemplateID,
		IsRecalled: true,
		RecallTime: nowMs,
	}, nil
}

// rewardsToGmt 将模型奖励列表转换为 GMT 共享 DTO 奖励列表
func rewardsToGmt(rewards []model.Reward) []gmtdto.GmtReward {
	if len(rewards) == 0 {
		return []gmtdto.GmtReward{}
	}
	result := make([]gmtdto.GmtReward, len(rewards))
	for i, r := range rewards {
		result[i] = gmtdto.GmtReward{ItemID: r.ItemID, Count: r.Count}
	}
	return result
}

// maxMailListOffset 邮件列表分页 offset 上限，防止超大偏移导致深分页拖垮数据库
const maxMailListOffset = 100

// lazyCreateTimeout 系统邮件 Lazy Create 独立超时；
// 单飞（singleflight）共享首个请求上下文，若首个请求被取消会拖垮同 playerID 的并发请求，
// 因此 Lazy Create 使用独立的超时上下文，仅依赖此超时而非任意单一请求的生命周期
const lazyCreateTimeout = 5 * time.Second

// GetMailList 获取玩家邮件列表，执行 Lazy Create 补全未下发的系统邮件
// req: proto 请求消息（分页参数）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *MailLogic) GetMailList(ctx context.Context, req *cspb.MailListReq, requestID string, playerID int64) (*cspb.MailListResp, error) {
	templateRepo, mailRepo := l.repos()
	now := time.Now().UnixMilli()

	start, end := req.Start, req.End
	if start < 0 {
		start = 0
	}
	if end <= 0 {
		end = 19
	}
	if end < start {
		end = start + 19
	}
	offset := start
	if offset > maxMailListOffset {
		offset = maxMailListOffset
	}
	limit := min(end-start+1, int64(50))

	lazyCtx, cancel := context.WithTimeout(context.Background(), lazyCreateTimeout)
	defer cancel()
	_, err, _ := l.lazyCreateGroup.Do(strconv.FormatInt(playerID, 10), func() (any, error) {
		return nil, l.lazyCreateMails(lazyCtx, playerID, now, templateRepo, mailRepo, requestID)
	})
	if err != nil {
		logx.Warn("系统邮件 Lazy Create 失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
	}

	mails, err := mailRepo.FindMailList(ctx, playerID, now, offset, limit)
	if err != nil {
		logx.Error("查询邮件列表失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询邮件列表失败: %w", err)
	}

	items := make([]*cspb.MailListItem, 0, len(mails))
	for _, m := range mails {
		items = append(items, &cspb.MailListItem{
			MailId:     m.MailID,
			Title:      textContentToProto(m.Title),
			Sender:     m.Sender,
			HasReward:  m.HasReward(),
			IsRead:     m.IsRead(),
			IsRewarded: m.IsRewarded(),
			CreateTime: m.CreateTime,
			ExpireTime: m.ExpireTime,
		})
	}

	return &cspb.MailListResp{
		Data: &cspb.MailListData{Mails: items},
	}, nil
}

func (l *MailLogic) generateMailIDs(count int) []int64 {
	ids := make([]int64, count)
	for i := range ids {
		ids[i] = l.idGen.NextID()
	}
	return ids
}

// ReadMail 查看邮件详情，标记已读
// req: proto 请求消息（mailId）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *MailLogic) ReadMail(ctx context.Context, req *cspb.MailReadReq, requestID string, playerID int64) (*cspb.MailReadResp, error) {
	mailID := req.MailId
	_, mailRepo := l.repos()

	mail, err := mailRepo.FindByMailID(ctx, playerID, mailID, time.Now().UnixMilli())
	if errors.Is(err, mongox.ErrNotFound) {
		return nil, model.ErrMailNotFound
	}
	if err != nil {
		logx.Error("查询邮件失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("mailId", mailID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询邮件失败: %w", err)
	}

	if !mail.IsRead() {
		if _, updateErr := mailRepo.UpdateStatus(ctx, playerID, mailID, model.MailRead); updateErr != nil {
			logx.Warn("标记邮件已读失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Int64("mailId", mailID),
				zap.Error(updateErr),
			)
		} else {
			mail.MarkRead()
		}
	}

	return &cspb.MailReadResp{
		Data: &cspb.MailDetailData{
			MailId:     mail.MailID,
			Title:      textContentToProto(mail.Title),
			Content:    textContentToProto(mail.Content),
			Sender:     mail.Sender,
			Rewards:    modelRewardsToProto(mail.Rewards),
			IsRead:     mail.IsRead(),
			IsRewarded: mail.IsRewarded(),
			CreateTime: mail.CreateTime,
			ExpireTime: mail.ExpireTime,
		},
	}, nil
}

// ReceiveMail 领取单封邮件附件
// req: proto 请求消息（mailId）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *MailLogic) ReceiveMail(ctx context.Context, req *cspb.MailReceiveReq, requestID string, playerID int64) (*cspb.MailReceiveResp, error) {
	mailID := req.MailId
	_, mailRepo := l.repos()

	mail, err := mailRepo.FindByMailID(ctx, playerID, mailID, time.Now().UnixMilli())
	if errors.Is(err, mongox.ErrNotFound) {
		return nil, model.ErrMailNotFound
	}
	if err != nil {
		logx.Error("查询邮件失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("mailId", mailID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询邮件失败: %w", err)
	}

	if !mail.HasReward() {
		return nil, model.ErrMailNoReward
	}
	if mail.IsRewarded() {
		return nil, model.ErrMailAlreadyRewarded
	}

	rewards := mail.Rewards
	if rewards == nil {
		rewards = []model.Reward{}
	}

	// 奖励由客户端发放，服务端仅记录领取状态（置位已领取标记）
	modified, updateErr := mailRepo.ClaimReward(ctx, playerID, mailID)
	if updateErr != nil {
		logx.Error("更新邮件领取状态失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("mailId", mailID),
			zap.Error(updateErr),
		)
		return nil, fmt.Errorf("更新邮件状态失败: %w", updateErr)
	}
	if modified == 0 {
		return nil, model.ErrMailAlreadyRewarded
	}

	logx.Info("玩家领取邮件附件",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.Int64("mailId", mailID),
		zap.Int("rewardCount", len(rewards)),
	)

	return &cspb.MailReceiveResp{
		Data: &cspb.MailReceiveData{Rewards: modelRewardsToProto(rewards)},
	}, nil
}

// ReceiveAllMails 一键领取所有可领取的邮件附件
// req: proto 请求消息（无业务字段）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *MailLogic) ReceiveAllMails(ctx context.Context, req *cspb.MailReceiveAllReq, requestID string, playerID int64) (*cspb.MailReceiveAllResp, error) {
	_, mailRepo := l.repos()
	now := time.Now().UnixMilli()

	mails, err := mailRepo.FindUnreceivedMails(ctx, playerID, now)
	if err != nil {
		logx.Error("查询未领取邮件失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询邮件失败: %w", err)
	}

	rewardMap := make(map[string]int64)
	var claimedCount int
	for _, m := range mails {
		// 奖励由客户端发放，服务端仅记录领取状态（置位已领取标记）
		modified, claimErr := mailRepo.ClaimReward(ctx, playerID, m.MailID)
		if claimErr != nil {
			logx.Warn("一键领取单封邮件失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Int64("mailId", m.MailID),
				zap.Error(claimErr),
			)
			continue
		}
		if modified == 0 {
			continue
		}
		claimedCount++
		for _, r := range m.Rewards {
			rewardMap[r.ItemID] += r.Count
		}
	}

	if _, markErr := mailRepo.MarkNoRewardMailsAsRead(ctx, playerID); markErr != nil {
		logx.Warn("批量标记无附件邮件已读失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(markErr),
		)
	}

	mergedRewards := mergeRewards(rewardMap)
	logx.Info("玩家一键领取邮件附件",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.Int("mailCount", claimedCount),
		zap.Int("rewardTypes", len(mergedRewards)),
	)

	return &cspb.MailReceiveAllResp{
		Data: &cspb.MailReceiveAllData{
			Rewards: modelRewardsToProto(mergedRewards),
			Count:   int32(claimedCount),
		},
	}, nil
}

// DeleteReadMails 删除玩家所有已读且可清理的邮件
// req: proto 请求消息（无业务字段）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *MailLogic) DeleteReadMails(ctx context.Context, req *cspb.MailDeleteReq, requestID string, playerID int64) (*cspb.MailDeleteResp, error) {
	_, mailRepo := l.repos()

	deleted, err := mailRepo.DeleteReadMails(ctx, playerID)
	if err != nil {
		logx.Error("清理已读邮件失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("清理邮件失败: %w", err)
	}

	logx.Info("玩家清理已读邮件",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.Int64("deleted", deleted),
	)
	return &cspb.MailDeleteResp{Deleted: deleted}, nil
}

// expiredMailRetentionPeriod 邮件过期后的清理宽限期：过期超过该时长的邮件由定时任务删除
const expiredMailRetentionPeriod = 7 * 24 * time.Hour

// CleanupExpiredMails 清理过期超过宽限期（默认7天）的邮件（定时任务）
// 按批次删除控制单次操作规模，单次任务最多处理 maxCleanupBatches 批；
// 若存量过大未清理完，下一次调度周期会继续，保证不阻塞服务。
func (l *MailLogic) CleanupExpiredMails(ctx context.Context) error {
	_, mailRepo := l.repos()
	// 清理阈值：当前时刻往前推一个宽限期，expireTime 早于该值的邮件视为"过期超过7天"
	beforeMs := time.Now().UnixMilli() - expiredMailRetentionPeriod.Milliseconds()

	const batchSize = 200
	const maxCleanupBatches = 100
	var total int64
	for batch := 0; batch < maxCleanupBatches; batch++ {
		deleted, err := mailRepo.DeleteExpiredMails(ctx, beforeMs, batchSize)
		if err != nil {
			logx.Error("清理过期邮件失败",
				zap.Int64("beforeMs", beforeMs),
				zap.Int64("batch", int64(batch+1)),
				zap.Error(err),
			)
			return fmt.Errorf("清理过期邮件失败: %w", err)
		}
		total += deleted
		if deleted == 0 {
			break
		}
	}

	logx.Info("过期邮件清理完成",
		zap.Int64("beforeMs", beforeMs),
		zap.Int64("deleted", total),
	)
	return nil
}

func (l *MailLogic) lazyCreateMails(
	ctx context.Context,
	playerID int64,
	nowMs int64,
	templateRepo *repo.MailTemplateRepo,
	mailRepo *repo.PlayerMailRepo,
	requestID string,
) error {
	templates, err := templateRepo.FindTemplatesForPlayer(ctx, playerID, nowMs)
	if err != nil {
		return fmt.Errorf("拉取邮件模板失败: %w", err)
	}
	if len(templates) == 0 {
		return nil
	}

	templateIDs := make([]int64, len(templates))
	for i, t := range templates {
		templateIDs[i] = t.TemplateID
	}
	existingIDs, err := mailRepo.FindExistingTemplateIDs(ctx, playerID, templateIDs)
	if err != nil {
		return fmt.Errorf("查询已有邮件模板ID失败: %w", err)
	}

	currentCount, err := mailRepo.CountByPlayerID(ctx, playerID)
	if err != nil {
		return fmt.Errorf("统计邮件数量失败: %w", err)
	}

	var toInsert []*model.PlayerMail
	for _, t := range templates {
		if _, ok := existingIDs[t.TemplateID]; ok {
			continue
		}
		toInsert = append(toInsert, &model.PlayerMail{
			PlayerID:   playerID,
			TemplateID: t.TemplateID,
			Title:      t.Title,
			Content:    t.Content,
			Sender:     t.Sender,
			Rewards:    t.Rewards,
			Status:     0,
			CreateTime: nowMs,
			// 相对有效期换算为绝对过期时间戳：从邮件下发（玩家收到）时刻开始计算
			ExpireTime: mailExpireTime(nowMs, t.ExpireTime),
		})
	}

	if len(toInsert) == 0 {
		return nil
	}

	newTotal := currentCount + int64(len(toInsert))
	if newTotal > model.MailBoxMaxSize {
		overflow := newTotal - model.MailBoxMaxSize
		cleaned, cleanErr := mailRepo.CleanupOverflowMails(ctx, playerID, overflow)
		if cleanErr != nil {
			logx.Warn("邮箱溢出清理失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Int64("overflow", overflow),
				zap.Error(cleanErr),
			)
			cleaned = 0
		}
		canInsert := max(model.MailBoxMaxSize-currentCount+cleaned, 0)
		if canInsert < int64(len(toInsert)) {
			dropped := toInsert[canInsert:]
			droppedIDs := make([]int64, len(dropped))
			for i, m := range dropped {
				droppedIDs[i] = m.TemplateID
			}
			logx.Warn("邮箱已满，部分系统邮件模板被丢弃",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Int64s("droppedTemplateIds", droppedIDs),
			)
			toInsert = toInsert[:canInsert]
		}
	}

	if len(toInsert) == 0 {
		return nil
	}

	mailIDs := l.generateMailIDs(len(toInsert))
	for i, m := range toInsert {
		m.MailID = mailIDs[i]
	}

	inserted, err := mailRepo.InsertManyMails(ctx, toInsert)
	if err != nil {
		return fmt.Errorf("批量创建玩家邮件失败: %w", err)
	}

	logx.Info("系统邮件 Lazy Create 完成",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.Int64("created", inserted),
	)
	return nil
}

func mergeRewards(rewardMap map[string]int64) []model.Reward {
	if len(rewardMap) == 0 {
		return []model.Reward{}
	}
	rewards := make([]model.Reward, 0, len(rewardMap))
	for itemID, count := range rewardMap {
		rewards = append(rewards, model.Reward{ItemID: itemID, Count: count})
	}
	slices.SortFunc(rewards, func(a, b model.Reward) int {
		return strings.Compare(a.ItemID, b.ItemID)
	})
	return rewards
}

func modelRewardsToProto(rewards []model.Reward) []*cspb.Reward {
	if len(rewards) == 0 {
		return []*cspb.Reward{}
	}
	result := make([]*cspb.Reward, len(rewards))
	for i, r := range rewards {
		result[i] = &cspb.Reward{ItemId: r.ItemID, Count: r.Count}
	}
	return result
}

// textContentToProto 将模型的文本内容转换为客户端 proto 结构
func textContentToProto(t model.TextContent) *cspb.TextContent {
	return &cspb.TextContent{
		Type: cspb.TextType(t.Type),
		Text: t.Text,
	}
}

// defaultMailExpireMs 玩家邮件默认相对有效期（毫秒，7 天）：
// 模板未配置过期时间（expireTime<=0）时，创建玩家邮件仍按 7 天计算过期时间，保证邮件必有过期时间
const defaultMailExpireMs = 7 * 24 * 60 * 60 * 1000

// mailExpireTime 将模板相对有效期（毫秒）换算为玩家邮件的绝对过期时间戳
// createTimeMs: 邮件下发（玩家收到）时刻（Unix毫秒）；relativeMs: 相对有效期，<=0 时使用默认 7 天
func mailExpireTime(createTimeMs, relativeMs int64) int64 {
	if relativeMs <= 0 {
		relativeMs = defaultMailExpireMs
	}
	return createTimeMs + relativeMs
}
