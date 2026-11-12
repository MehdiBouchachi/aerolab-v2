// Device adapters. NOT exercised in tests (they need a phone): the logic that uses them is tested against MemoryStore.
import { createAsyncStorage } from '@react-native-async-storage/async-storage'
import * as Crypto from 'expo-crypto'
import * as SecureStore from 'expo-secure-store'
import type { KeyValueStore } from './core/types'

/** Session tokens: OS keychain/keystore, this device only, available when unlocked. */
export const secureStore: KeyValueStore = {
  get: k => SecureStore.getItemAsync(sk(k)),
  set: (k, v) => SecureStore.setItemAsync(sk(k), v, { keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY }),
  remove: k => SecureStore.deleteItemAsync(sk(k)),
}
// SecureStore keys allow only [A-Za-z0-9._-]
const sk = (k: string) => k.replace(/[^A-Za-z0-9._-]/g, '_')

const as = createAsyncStorage('aerolab')
const INDEX = '__index'
/** Read-model cache + pairings. A key index is kept because AsyncStorage cannot list keys cheaply. */
export const cacheStore: KeyValueStore = {
  get: k => as.getItem(k),
  async set(k, v) {
    await as.setItem(k, v)
    const idx: string[] = JSON.parse((await as.getItem(INDEX)) ?? '[]')
    if (!idx.includes(k)) { idx.push(k); await as.setItem(INDEX, JSON.stringify(idx)) }
  },
  async remove(k) {
    await as.removeItem(k)
    const idx: string[] = JSON.parse((await as.getItem(INDEX)) ?? '[]')
    await as.setItem(INDEX, JSON.stringify(idx.filter(x => x !== k)))
  },
  async keys() { return JSON.parse((await as.getItem(INDEX)) ?? '[]') as string[] },
}

export const random = (n: number): Uint8Array => Crypto.getRandomBytes(n)
