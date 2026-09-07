import type { ReactNode } from "react";
import { type Navigate, type RouteState } from "../routing";
import { AccountDetailPage } from "./AccountDetailPage";
import { AccountsPage } from "./AccountsPage";
import { SharedDevicesPage } from "./SharedDevicesPage";
import { CollectibleUsernameDetailPage } from "./CollectibleUsernameDetailPage";
import { CollectibleUsernamesPage } from "./CollectibleUsernamesPage";
import { ChannelDetailPage } from "./ChannelDetailPage";
import { ChannelsPage } from "./ChannelsPage";
import { BotDetailPage } from "./BotDetailPage";
import { BotsPage } from "./BotsPage";
import { BroadcastsPage } from "./BroadcastsPage";
import { Dashboard } from "./Dashboard";
import { GroupMessageDetailPage } from "./GroupMessageDetailPage";
import { MessageDetailPage } from "./MessageDetailPage";
import { MessagesPage } from "./MessagesPage";
import { StickerSetsPage } from "./StickerSetsPage";
import { GifCatalogPage } from "./GifCatalogPage";
import { AdminUsersPage } from "./AdminUsersPage";
import { ServerSettingsPage } from "./ServerSettingsPage";
import { ModerationCaseDetailPage } from "./ModerationCaseDetailPage";
import { ModerationCasesPage } from "./ModerationCasesPage";
import { StoragePage } from "./StoragePage";
import { BotVerificationPage } from "./BotVerificationPage";
import { BotVerificationRequestPage } from "./BotVerificationRequestPage";
import { VerificationDetailPage } from "./VerificationDetailPage";
import { VerificationPage } from "./VerificationPage";
import {
  PermissionGate,
  ThirdPartyVerificationHiddenGate,
  permissionAccountsRead,
  permissionAdminsManage,
  permissionBotVerificationReview,
  permissionBotsRead,
  permissionBroadcastsRead,
  permissionChannelsRead,
  permissionContentRead,
  permissionDashboardRead,
  permissionMessagesRead,
  permissionModerationReview,
  permissionServerManage,
  permissionStorageRead,
  permissionUsernamesRead,
  permissionVerificationReview
} from "../permissions";

export function Routes({ route, navigate }: { route: RouteState; navigate: Navigate }) {
  // Every section is wrapped in the right it needs. Without this the page
  // rendered, fired its request, and showed the backend's "permission X is
  // required" as a red bar over an empty table -- an error where a refusal
  // belongs. Gating here means the request is never made either.
  const gate = (permission: string, node: ReactNode) => (
    <PermissionGate navigate={navigate} permission={permission}>{node}</PermissionGate>
  );
  const accountID = route.path.match(/^\/accounts\/(\d+)$/)?.[1];
  const channelID = route.path.match(/^\/channels\/(\d+)$/)?.[1];
  const botID = route.path.match(/^\/bots\/(\d+)$/)?.[1];
  const moderationCaseID = route.path.match(/^\/moderation\/(\d+)$/)?.[1];
  // int64 ids stay strings so large values never lose precision.
  const collectibleUsernameID = route.path.match(/^\/collectible-usernames\/(\d+)$/)?.[1];
  const verificationID = route.path.match(/^\/verification\/(\d+)$/)?.[1];
  // Third-party verification: a separate section with its own rights, matched before
  // the official one so neither prefix can shadow the other.
  const botVerificationRequestID = route.path.match(/^\/bot-verification\/(\d+)$/)?.[1];
  if (botVerificationRequestID) {
    return (
      <ThirdPartyVerificationHiddenGate navigate={navigate}>
        <PermissionGate navigate={navigate} permission={permissionBotVerificationReview}>
          <BotVerificationRequestPage id={botVerificationRequestID} navigate={navigate} />
        </PermissionGate>
      </ThirdPartyVerificationHiddenGate>
    );
  }
  if (route.path === "/bot-verification") {
    return (
      <ThirdPartyVerificationHiddenGate navigate={navigate}>
        <PermissionGate navigate={navigate} permission={permissionBotVerificationReview}>
          <BotVerificationPage navigate={navigate} />
        </PermissionGate>
      </ThirdPartyVerificationHiddenGate>
    );
  }
  // The detail match has to be tested before the exact "/verification" branch, and
  // the whole section is wrapped in the permission gate so a direct URL explains
  // itself instead of rendering an empty queue.
  if (verificationID) {
    return (
      <PermissionGate navigate={navigate} permission={permissionVerificationReview}>
        <VerificationDetailPage id={verificationID} navigate={navigate} />
      </PermissionGate>
    );
  }
  if (route.path === "/verification") {
    return (
      <PermissionGate navigate={navigate} permission={permissionVerificationReview}>
        <VerificationPage navigate={navigate} />
      </PermissionGate>
    );
  }
  if (collectibleUsernameID) {
    return gate(permissionUsernamesRead, <CollectibleUsernameDetailPage id={collectibleUsernameID} navigate={navigate} />);
  }
  if (route.path === "/collectible-usernames") {
    return gate(permissionUsernamesRead, <CollectibleUsernamesPage navigate={navigate} />);
  }
  if (route.path === "/storage") {
    return gate(permissionStorageRead, <StoragePage navigate={navigate} />);
  }
  if (accountID) {
    return gate(permissionAccountsRead, <AccountDetailPage id={Number(accountID)} navigate={navigate} />);
  }
  if (channelID) {
    return gate(permissionChannelsRead, <ChannelDetailPage id={Number(channelID)} navigate={navigate} />);
  }
  if (botID) {
    return gate(permissionBotsRead, <BotDetailPage id={Number(botID)} navigate={navigate} />);
  }
  if (moderationCaseID) {
    return gate(permissionModerationReview, <ModerationCaseDetailPage id={Number(moderationCaseID)} navigate={navigate} />);
  }
  if (route.path === "/accounts/shared-devices") {
    return gate(permissionAccountsRead, <SharedDevicesPage navigate={navigate} />);
  }
  if (route.path === "/accounts") {
    return gate(permissionAccountsRead, <AccountsPage navigate={navigate} />);
  }
  if (route.path === "/channels") {
    return gate(permissionChannelsRead, <ChannelsPage navigate={navigate} />);
  }
  if (route.path === "/bots") {
    return gate(permissionBotsRead, <BotsPage navigate={navigate} />);
  }
  if (route.path === "/moderation") {
    return gate(permissionModerationReview, <ModerationCasesPage navigate={navigate} />);
  }
  if (route.path === "/broadcasts") {
    return gate(permissionBroadcastsRead, <BroadcastsPage />);
  }
  if (route.path === "/emoji") {
    return gate(permissionContentRead, <StickerSetsPage kind="emoji" />);
  }
	if (route.path === "/stickers") {
		return gate(permissionContentRead, <StickerSetsPage kind="stickers" />);
	}
  if (route.path === "/gif-catalog") {
    return gate(permissionContentRead, <GifCatalogPage />);
  }
  if (route.path === "/admin-users") {
    return (
      <PermissionGate navigate={navigate} permission={permissionAdminsManage}>
        <AdminUsersPage />
      </PermissionGate>
    );
  }
  if (route.path === "/server-settings") {
    return (
      <PermissionGate navigate={navigate} permission={permissionServerManage}>
        <ServerSettingsPage />
      </PermissionGate>
    );
  }
  if (route.path === "/messages/detail" || route.path === "/messages/private/detail") {
    return gate(permissionMessagesRead, (
      <MessageDetailPage
        ownerUserID={Number(route.search.get("owner_user_id") || "0")}
        msgID={Number(route.search.get("msg_id") || "0")}
        navigate={navigate}
      />
    ));
  }
  if (route.path === "/messages/groups/detail") {
    return gate(permissionMessagesRead, (
      <GroupMessageDetailPage
        channelID={Number(route.search.get("channel_id") || "0")}
        msgID={Number(route.search.get("msg_id") || "0")}
        navigate={navigate}
      />
    ));
  }
  // Both tabs keep their own path so a link to one still opens on it -- the
  // tab is a view of /messages, not a hidden bit of component state.
  if (route.path === "/messages" || route.path === "/messages/private" || route.path === "/messages/groups") {
    return gate(permissionMessagesRead, (
      <MessagesPage
        navigate={navigate}
        tab={route.path === "/messages/groups" ? "groups" : "private"}
        onTab={(tab) => navigate(tab === "groups" ? "/messages/groups" : "/messages/private")}
      />
    ));
  }
  return gate(permissionDashboardRead, <Dashboard navigate={navigate} />);
}
