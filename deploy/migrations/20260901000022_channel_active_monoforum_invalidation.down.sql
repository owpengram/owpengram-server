DROP TRIGGER IF EXISTS channels_monoforum_active_membership_changed ON public.channels;
DROP FUNCTION IF EXISTS public.telesrv_notify_monoforum_channel_visibility_read_model();
DROP FUNCTION IF EXISTS public.telesrv_bump_monoforum_visibility_owners(bigint, bigint);

DROP TRIGGER IF EXISTS channel_members_monoforum_manager_visibility_changed ON public.channel_members;
DROP FUNCTION IF EXISTS public.telesrv_notify_monoforum_manager_visibility_read_model();

DROP TRIGGER IF EXISTS channel_messages_monoforum_active_membership_changed ON public.channel_messages;
DROP FUNCTION IF EXISTS public.telesrv_notify_monoforum_message_visibility_read_model();
DROP FUNCTION IF EXISTS public.telesrv_bump_channel_active_membership_owner(bigint);
