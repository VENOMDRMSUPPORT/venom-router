
CREATE OR REPLACE FUNCTION public.set_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql SET search_path = public AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$;

REVOKE EXECUTE ON FUNCTION public.has_role(UUID, public.app_role) FROM PUBLIC, anon, authenticated;
REVOKE EXECUTE ON FUNCTION public.is_owner() FROM PUBLIC, anon, authenticated;
REVOKE EXECUTE ON FUNCTION public.handle_first_user() FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.is_owner() TO authenticated;
GRANT EXECUTE ON FUNCTION public.has_role(UUID, public.app_role) TO authenticated;
