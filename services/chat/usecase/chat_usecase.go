package usecase

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tachyon-messenger/services/chat/models"
	"tachyon-messenger/services/chat/repository"

	"gorm.io/gorm"
)

// ChatUsecase defines the interface for chat business logic
type ChatUsecase interface {
	CreateChat(userID uint, req *models.CreateChatRequest) (*models.ChatResponse, error)
	GetUserChats(userID uint, limit, offset int) (*models.ChatListResponse, error)
	GetChat(userID, chatID uint) (*models.ChatResponse, error)
	UpdateChat(userID, chatID uint, req *models.UpdateChatRequest) (*models.ChatResponse, error)
	DeleteChat(userID, chatID uint) error
	AddMember(userID, chatID uint, req *models.AddChatMemberRequest) error
	RemoveMember(userID, chatID, targetUserID uint) error
	GetChatMembers(userID, chatID uint) ([]models.ChatMemberResponse, error)
	LeaveChat(userID, chatID uint) error
	CreatePersonalChat(userID, targetUserID uint) (*models.ChatResponse, error)
	CreateGroupChat(userID uint, req *models.CreateGroupChatRequest) (*models.ChatResponse, error)
	JoinChat(userID, chatID uint) error
}

// chatUsecase implements ChatUsecase interface
type chatUsecase struct {
	chatRepo    repository.ChatRepository
	messageRepo repository.MessageRepository
}

func (uc *chatUsecase) CreatePersonalChat(userID, targetUserID uint) (*models.ChatResponse, error) {
	// Validate input
	if userID == targetUserID {
		return nil, fmt.Errorf("cannot create personal chat with yourself")
	}

	// Check if personal chat already exists between these users
	existingChats, err := uc.chatRepo.GetByUserID(userID, 100, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing chats: %w", err)
	}

	for _, chat := range existingChats {
		if chat.Type == models.ChatTypePrivate {
			members, err := uc.chatRepo.GetChatMembers(chat.ID)
			if err != nil {
				continue
			}
			if len(members) == 2 {
				for _, member := range members {
					if member.UserID == targetUserID {
						// Chat already exists, return it
						return uc.GetChat(userID, chat.ID)
					}
				}
			}
		}
	}

	// Create new personal chat
	chat := &models.Chat{
		Name:        "", // Personal chats don't have names
		Description: "",
		Type:        models.ChatTypePrivate,
		CreatorID:   userID,
		IsActive:    true,
	}

	if err := uc.chatRepo.Create(chat); err != nil {
		return nil, fmt.Errorf("failed to create personal chat: %w", err)
	}

	// Add target user as member
	member := &models.ChatMember{
		ChatID:   chat.ID,
		UserID:   targetUserID,
		Role:     models.ChatMemberRoleMember,
		JoinedAt: time.Now(),
		IsActive: true,
	}

	if err := uc.chatRepo.AddMember(member); err != nil {
		return nil, fmt.Errorf("failed to add target user to personal chat: %w", err)
	}

	// Get chat with members for response
	chatWithMembers, err := uc.chatRepo.GetWithMembers(chat.ID)
	if err != nil {
		return chat.ToResponse(), nil // Return what we have
	}

	return chatWithMembers.ToResponse(), nil
}

// CreateGroupChat creates a group chat with multiple users
func (uc *chatUsecase) CreateGroupChat(userID uint, req *models.CreateGroupChatRequest) (*models.ChatResponse, error) {
	// Validate request
	if err := uc.validateCreateGroupChatRequest(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Ensure creator is not in member list (will be added as owner automatically)
	memberIDs := make([]uint, 0, len(req.MemberIDs))
	for _, memberID := range req.MemberIDs {
		if memberID != userID {
			memberIDs = append(memberIDs, memberID)
		}
	}

	// Create group chat
	chat := &models.Chat{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Type:        models.ChatTypeGroup,
		CreatorID:   userID,
		IsActive:    true,
	}

	if err := uc.chatRepo.Create(chat); err != nil {
		return nil, fmt.Errorf("failed to create group chat: %w", err)
	}

	// Add all members to the chat
	for _, memberID := range memberIDs {
		member := &models.ChatMember{
			ChatID:   chat.ID,
			UserID:   memberID,
			Role:     models.ChatMemberRoleMember,
			JoinedAt: time.Now(),
			IsActive: true,
		}

		if err := uc.chatRepo.AddMember(member); err != nil {
			// Log error but continue adding other members
			continue
		}
	}

	// Get chat with members for response
	chatWithMembers, err := uc.chatRepo.GetWithMembers(chat.ID)
	if err != nil {
		return chat.ToResponse(), nil // Return what we have
	}

	return chatWithMembers.ToResponse(), nil
}

// JoinChat allows a user to join an existing chat
func (uc *chatUsecase) JoinChat(userID, chatID uint) error {
	// Check if chat exists and is active
	chat, err := uc.chatRepo.GetByID(chatID)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}

	if !chat.IsActive {
		return fmt.Errorf("chat is not active")
	}

	// Check if user is already a member
	isMember, err := uc.chatRepo.IsMember(chatID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}

	if isMember {
		return fmt.Errorf("user is already a member of this chat")
	}

	// Check chat type restrictions
	if chat.Type == models.ChatTypePrivate {
		return fmt.Errorf("cannot join private chat")
	}

	// For group chats, check if user can join (add business logic as needed)
	if chat.Type == models.ChatTypeGroup {
		// Check member count limit if needed
		members, err := uc.chatRepo.GetChatMembers(chatID)
		if err != nil {
			return fmt.Errorf("failed to get chat members: %w", err)
		}

		// Example: limit group chat to 100 members
		if len(members) >= 100 {
			return fmt.Errorf("chat has reached maximum member limit")
		}
	}

	// Add user as member
	member := &models.ChatMember{
		ChatID:   chatID,
		UserID:   userID,
		Role:     models.ChatMemberRoleMember,
		JoinedAt: time.Now(),
		IsActive: true,
	}

	if err := uc.chatRepo.AddMember(member); err != nil {
		return fmt.Errorf("failed to join chat: %w", err)
	}

	return nil
}

// LeaveChat allows a user to leave a chat (updated version)
func (uc *chatUsecase) LeaveChat(userID, chatID uint) error {
	// Check if user is a member
	isMember, err := uc.chatRepo.IsMember(chatID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if !isMember {
		return fmt.Errorf("user is not a member of this chat")
	}

	// Get user role
	role, err := uc.chatRepo.GetMemberRole(chatID, userID)
	if err != nil {
		return fmt.Errorf("failed to get user role: %w", err)
	}

	// Get chat info
	chat, err := uc.chatRepo.GetByID(chatID)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}

	// Special handling for private chats
	if chat.Type == models.ChatTypePrivate {
		// For private chats, leaving means deactivating the chat
		chat.IsActive = false
		if err := uc.chatRepo.Update(chat); err != nil {
			return fmt.Errorf("failed to deactivate private chat: %w", err)
		}
	}

	// Owner cannot leave without transferring ownership (for group chats)
	if role == models.ChatMemberRoleOwner && chat.Type == models.ChatTypeGroup {
		// Check if there are other members who can become owner
		members, err := uc.chatRepo.GetChatMembers(chatID)
		if err != nil {
			return fmt.Errorf("failed to get chat members: %w", err)
		}

		// If only owner left, delete the chat
		if len(members) <= 1 {
			return uc.DeleteChat(userID, chatID)
		}

		// Find an admin to promote to owner, or promote the first member
		var newOwner *models.ChatMember
		for _, member := range members {
			if member.UserID != userID {
				if member.Role == models.ChatMemberRoleAdmin {
					newOwner = member
					break
				} else if newOwner == nil {
					newOwner = member
				}
			}
		}

		if newOwner != nil {
			// Promote to owner
			newOwner.Role = models.ChatMemberRoleOwner
			// Note: You might need to implement UpdateMember method in repository
			// For now, we'll remove and re-add with new role
			if err := uc.chatRepo.RemoveMember(chatID, newOwner.UserID); err != nil {
				return fmt.Errorf("failed to update new owner role: %w", err)
			}
			newOwner.JoinedAt = time.Now()
			if err := uc.chatRepo.AddMember(newOwner); err != nil {
				return fmt.Errorf("failed to set new owner: %w", err)
			}
		}
	}

	// Remove user from chat
	if err := uc.chatRepo.RemoveMember(chatID, userID); err != nil {
		return fmt.Errorf("failed to leave chat: %w", err)
	}

	return nil
}

// validateCreateGroupChatRequest validates group chat creation request
func (uc *chatUsecase) validateCreateGroupChatRequest(req *models.CreateGroupChatRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}

	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("group name is required")
	}

	if len(req.Name) > 255 {
		return fmt.Errorf("group name too long (max 255 characters)")
	}

	if len(req.Description) > 500 {
		return fmt.Errorf("description too long (max 500 characters)")
	}

	if len(req.MemberIDs) == 0 {
		return fmt.Errorf("at least one member is required for group chat")
	}

	if len(req.MemberIDs) > 99 { // +1 for creator = 100 max
		return fmt.Errorf("too many members (max 99 additional members)")
	}

	return nil
}

// NewChatUsecase creates a new chat usecase
func NewChatUsecase(chatRepo repository.ChatRepository, messageRepo repository.MessageRepository) ChatUsecase {
	return &chatUsecase{
		chatRepo:    chatRepo,
		messageRepo: messageRepo,
	}
}

// Chat Usecase Methods

// CreateChat creates a new chat
func (uc *chatUsecase) CreateChat(userID uint, req *models.CreateChatRequest) (*models.ChatResponse, error) {
	// Validate request
	if err := uc.validateCreateChatRequest(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// For private chats, ensure only 2 members (including creator)
	if req.Type == models.ChatTypePrivate {
		if len(req.MemberIDs) != 1 {
			return nil, fmt.Errorf("private chat must have exactly 2 members")
		}
		// Check if private chat already exists between these users
		existingChats, err := uc.chatRepo.GetByUserID(userID, 100, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing chats: %w", err)
		}

		for _, chat := range existingChats {
			if chat.Type == models.ChatTypePrivate {
				members, err := uc.chatRepo.GetChatMembers(chat.ID)
				if err != nil {
					continue
				}
				if len(members) == 2 {
					for _, member := range members {
						if member.UserID == req.MemberIDs[0] {
							return nil, fmt.Errorf("private chat already exists between these users")
						}
					}
				}
			}
		}
	}

	// Create chat
	chat := &models.Chat{
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		CreatorID:   userID,
		Avatar:      req.Avatar,
		IsActive:    true,
	}

	// For private chats, set name to empty (will be handled by client)
	if req.Type == models.ChatTypePrivate {
		chat.Name = ""
	}

	if err := uc.chatRepo.Create(chat); err != nil {
		return nil, fmt.Errorf("failed to create chat: %w", err)
	}

	// ИСПРАВЛЕНИЕ: Исключаем создателя из списка участников
	// AfterCreate хук уже добавляет создателя как owner
	for _, memberID := range req.MemberIDs {
		// Пропускаем создателя чата - он уже добавлен как owner
		if memberID == userID {
			continue
		}

		member := &models.ChatMember{
			ChatID:   chat.ID,
			UserID:   memberID,
			Role:     models.ChatMemberRoleMember,
			JoinedAt: time.Now(),
			IsActive: true,
		}
		if err := uc.chatRepo.AddMember(member); err != nil {
			return nil, fmt.Errorf("failed to add member %d: %w", memberID, err)
		}
	}

	// Get chat with members for response
	chatWithMembers, err := uc.chatRepo.GetWithMembers(chat.ID)
	if err != nil {
		return chat.ToResponse(), nil // Return what we have
	}

	return chatWithMembers.ToResponse(), nil
}

// GetUserChats retrieves all chats for a user
func (uc *chatUsecase) GetUserChats(userID uint, limit, offset int) (*models.ChatListResponse, error) {
	// Set default pagination
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	chats, total, err := uc.chatRepo.GetUserChats(userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get user chats: %w", err)
	}

	// Convert to response format
	chatResponses := make([]models.ChatResponse, len(chats))
	for i, chat := range chats {
		chatResponses[i] = *chat.ToResponse()
	}

	return &models.ChatListResponse{
		Chats:  chatResponses,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// GetChat retrieves a specific chat
func (uc *chatUsecase) GetChat(userID, chatID uint) (*models.ChatResponse, error) {
	// Check if user is a member of the chat
	isMember, err := uc.chatRepo.IsMember(chatID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if !isMember {
		return nil, fmt.Errorf("user is not a member of this chat")
	}

	// Get chat with members
	chat, err := uc.chatRepo.GetWithMembers(chatID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("chat not found")
		}
		return nil, fmt.Errorf("failed to get chat: %w", err)
	}

	return chat.ToResponse(), nil
}

// UpdateChat updates a chat
func (uc *chatUsecase) UpdateChat(userID, chatID uint, req *models.UpdateChatRequest) (*models.ChatResponse, error) {
	// Check if user has permission to update chat
	role, err := uc.chatRepo.GetMemberRole(chatID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user role: %w", err)
	}
	if role != models.ChatMemberRoleOwner && role != models.ChatMemberRoleAdmin {
		return nil, fmt.Errorf("insufficient permissions to update chat")
	}

	// Get existing chat
	chat, err := uc.chatRepo.GetByID(chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chat: %w", err)
	}

	// Update fields
	if req.Name != nil {
		chat.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		chat.Description = strings.TrimSpace(*req.Description)
	}
	if req.Avatar != nil {
		chat.Avatar = strings.TrimSpace(*req.Avatar)
	}

	if err := uc.chatRepo.Update(chat); err != nil {
		return nil, fmt.Errorf("failed to update chat: %w", err)
	}

	// Get updated chat with members
	updatedChat, err := uc.chatRepo.GetWithMembers(chatID)
	if err != nil {
		return chat.ToResponse(), nil // Return what we have
	}

	return updatedChat.ToResponse(), nil
}

// DeleteChat deletes a chat
func (uc *chatUsecase) DeleteChat(userID, chatID uint) error {
	// Check if user is the owner
	role, err := uc.chatRepo.GetMemberRole(chatID, userID)
	if err != nil {
		return fmt.Errorf("failed to get user role: %w", err)
	}
	if role != models.ChatMemberRoleOwner {
		return fmt.Errorf("only chat owner can delete the chat")
	}

	if err := uc.chatRepo.Delete(chatID); err != nil {
		return fmt.Errorf("failed to delete chat: %w", err)
	}

	return nil
}

// AddMember adds a member to a chat
func (uc *chatUsecase) AddMember(userID, chatID uint, req *models.AddChatMemberRequest) error {
	// Check if user has permission to add members
	role, err := uc.chatRepo.GetMemberRole(chatID, userID)
	if err != nil {
		return fmt.Errorf("failed to get user role: %w", err)
	}
	if role != models.ChatMemberRoleOwner && role != models.ChatMemberRoleAdmin {
		return fmt.Errorf("insufficient permissions to add members")
	}

	// Check if target user is already a member
	isMember, err := uc.chatRepo.IsMember(chatID, req.UserID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if isMember {
		return fmt.Errorf("user is already a member of this chat")
	}

	// Set default role if not provided
	memberRole := req.Role
	if memberRole == "" {
		memberRole = models.ChatMemberRoleMember
	}

	// Create member
	member := &models.ChatMember{
		ChatID:   chatID,
		UserID:   req.UserID,
		Role:     memberRole,
		JoinedAt: time.Now(),
		IsActive: true,
	}

	if err := uc.chatRepo.AddMember(member); err != nil {
		return fmt.Errorf("failed to add member: %w", err)
	}

	return nil
}

// RemoveMember removes a member from a chat
func (uc *chatUsecase) RemoveMember(userID, chatID, targetUserID uint) error {
	// Check if user has permission to remove members
	role, err := uc.chatRepo.GetMemberRole(chatID, userID)
	if err != nil {
		return fmt.Errorf("failed to get user role: %w", err)
	}

	// Get target user role
	targetRole, err := uc.chatRepo.GetMemberRole(chatID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to get target user role: %w", err)
	}

	// Permission checks
	if role != models.ChatMemberRoleOwner && role != models.ChatMemberRoleAdmin {
		return fmt.Errorf("insufficient permissions to remove members")
	}

	// Admin cannot remove owner
	if role == models.ChatMemberRoleAdmin && targetRole == models.ChatMemberRoleOwner {
		return fmt.Errorf("admin cannot remove chat owner")
	}

	// Owner cannot be removed (must transfer ownership first)
	if targetRole == models.ChatMemberRoleOwner {
		return fmt.Errorf("cannot remove chat owner, transfer ownership first")
	}

	if err := uc.chatRepo.RemoveMember(chatID, targetUserID); err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}

	return nil
}

// GetChatMembers retrieves all members of a chat
func (uc *chatUsecase) GetChatMembers(userID, chatID uint) ([]models.ChatMemberResponse, error) {
	// Check if user is a member of the chat
	isMember, err := uc.chatRepo.IsMember(chatID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if !isMember {
		return nil, fmt.Errorf("user is not a member of this chat")
	}

	members, err := uc.chatRepo.GetChatMembers(chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chat members: %w", err)
	}

	// Convert to response format
	memberResponses := make([]models.ChatMemberResponse, len(members))
	for i, member := range members {
		memberResponses[i] = *member.ToResponse()
	}

	return memberResponses, nil
}

// validateCreateChatRequest validates chat creation request
func (uc *chatUsecase) validateCreateChatRequest(req *models.CreateChatRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}

	// Validate chat type
	if req.Type != models.ChatTypePrivate && req.Type != models.ChatTypeGroup && req.Type != models.ChatTypeChannel {
		return fmt.Errorf("invalid chat type")
	}

	// For group and channel chats, name is required
	if (req.Type == models.ChatTypeGroup || req.Type == models.ChatTypeChannel) && strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required for group and channel chats")
	}

	// Validate member IDs
	if len(req.MemberIDs) == 0 && req.Type != models.ChatTypeChannel {
		return fmt.Errorf("at least one member is required")
	}

	return nil
}
