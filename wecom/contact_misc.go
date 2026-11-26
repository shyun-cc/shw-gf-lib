package wecom

import (
	"context"
	"io"
	"net/http"
	"strconv"
)

// 客户联系子域⑥表项（域收官）：商品图册 5 + 聊天敏感词 5 + 上传附件 1 + 已服务外部联系人 1
// （官方清点见 change wecom-contact-misc-domain proposal）
var (
	epAlbumAdd       = Endpoint{"ec.album.add", http.MethodPost, "/cgi-bin/externalcontact/add_product_album", nil, true}
	epAlbumGet       = Endpoint{"ec.album.get", http.MethodPost, "/cgi-bin/externalcontact/get_product_album", nil, true}
	epAlbumList      = Endpoint{"ec.album.list", http.MethodPost, "/cgi-bin/externalcontact/get_product_album_list", nil, true}
	epAlbumUpdate    = Endpoint{"ec.album.update", http.MethodPost, "/cgi-bin/externalcontact/update_product_album", nil, true}
	epAlbumDelete    = Endpoint{"ec.album.delete", http.MethodPost, "/cgi-bin/externalcontact/delete_product_album", nil, true}
	epInterceptAdd   = Endpoint{"ec.intercept_rule.add", http.MethodPost, "/cgi-bin/externalcontact/add_intercept_rule", nil, true}
	epInterceptList  = Endpoint{"ec.intercept_rule.list", http.MethodGet, "/cgi-bin/externalcontact/get_intercept_rule_list", nil, false}
	epInterceptGet   = Endpoint{"ec.intercept_rule.get", http.MethodPost, "/cgi-bin/externalcontact/get_intercept_rule", nil, true}
	epInterceptEdit  = Endpoint{"ec.intercept_rule.update", http.MethodPost, "/cgi-bin/externalcontact/update_intercept_rule", nil, true}
	epInterceptDel   = Endpoint{"ec.intercept_rule.del", http.MethodPost, "/cgi-bin/externalcontact/del_intercept_rule", nil, true}
	epUploadAttach   = Endpoint{"media.upload_attachment", http.MethodPost, "/cgi-bin/media/upload_attachment", []string{"media_type", "attachment_type"}, true}
	epServedContacts = Endpoint{"ec.served_contact.list", http.MethodPost, "/cgi-bin/externalcontact/contact_list", nil, true}
)

// AddProductAlbum 创建商品图册（body：description/price/product_sn/attachments）
func (c *Client) AddProductAlbum(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAlbumAdd, nil, body)
}

// GetProductAlbum 获取商品图册
func (c *Client) GetProductAlbum(ctx context.Context, productID string) (map[string]any, error) {
	return c.invoke(ctx, epAlbumGet, nil, map[string]any{"product_id": productID})
}

// GetProductAlbumList 获取商品图册列表（body：limit/cursor）
func (c *Client) GetProductAlbumList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAlbumList, nil, body)
}

// UpdateProductAlbum 编辑商品图册
func (c *Client) UpdateProductAlbum(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAlbumUpdate, nil, body)
}

// DeleteProductAlbum 删除商品图册
func (c *Client) DeleteProductAlbum(ctx context.Context, productID string) (map[string]any, error) {
	return c.invoke(ctx, epAlbumDelete, nil, map[string]any{"product_id": productID})
}

// AddInterceptRule 新建敏感词规则（body：rule_name/word_list/semantics_list/intercept_type/applicable_range）
func (c *Client) AddInterceptRule(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epInterceptAdd, nil, body)
}

// GetInterceptRuleList 获取敏感词规则列表
func (c *Client) GetInterceptRuleList(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epInterceptList, nil, nil)
}

// GetInterceptRule 获取敏感词规则详情
func (c *Client) GetInterceptRule(ctx context.Context, ruleID string) (map[string]any, error) {
	return c.invoke(ctx, epInterceptGet, nil, map[string]any{"rule_id": ruleID})
}

// UpdateInterceptRule 修改敏感词规则
func (c *Client) UpdateInterceptRule(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epInterceptEdit, nil, body)
}

// DelInterceptRule 删除敏感词规则
func (c *Client) DelInterceptRule(ctx context.Context, ruleID string) (map[string]any, error) {
	return c.invoke(ctx, epInterceptDel, nil, map[string]any{"rule_id": ruleID})
}

// UploadAttachment 上传附件资源（multipart）；mediaType=image/video/file；attachmentType 1=朋友圈 2=商品图册
func (c *Client) UploadAttachment(ctx context.Context, mediaType string, attachmentType int, filename string, content io.Reader) (map[string]any, error) {
	return c.InvokeMultipart(ctx, epUploadAttach, map[string]string{"media_type": mediaType, "attachment_type": strconv.Itoa(attachmentType)}, filename, content)
}

// GetServedContactList 获取已服务的外部联系人（body：cursor/limit）
func (c *Client) GetServedContactList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epServedContacts, nil, body)
}
