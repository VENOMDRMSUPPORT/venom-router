INSERT INTO public.user_roles (user_id, role)
SELECT id, 'owner' FROM auth.users WHERE email = 'hamees77aly7@gmail.com'
ON CONFLICT (user_id, role) DO NOTHING;