import axios, { endpoints } from 'src/lib/axios';

import { setSession } from './utils';

// ----------------------------------------------------------------------

export type SignInParams = {
  username: string;
  password: string;
};

/** **************************************
 * Sign in
 *************************************** */
export type SignInResult = { mfaRequired: true; ticket: string } | { mfaRequired: false };
export const signInWithPassword = async ({
  username,
  password,
}: SignInParams): Promise<SignInResult> => {
  const { data } = await axios.post(endpoints.auth.signIn, { username, password });
  if (data.mfaRequired && typeof data.ticket === 'string')
    return { mfaRequired: true, ticket: data.ticket };
  if (!data.accessToken) throw new Error('登录响应里没有 accessToken');
  await setSession(data.accessToken);
  return { mfaRequired: false };
};
export const signInWithMFA = async (ticket: string, code: string): Promise<void> => {
  const { data } = await axios.post('/api/auth/mfa', { ticket, code });
  if (!data.accessToken) throw new Error('登录响应里没有 accessToken');
  await setSession(data.accessToken);
};

/** **************************************
 * Sign out
 *************************************** */
export const signOut = async (): Promise<void> => {
  try {
    await setSession(null);
  } catch (error) {
    console.error('Error during sign out:', error);
    throw error;
  }
};
