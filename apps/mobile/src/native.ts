// Device adapters. NOT exercised in tests (they need a phone): the logic that uses them is tested against MemoryStore.
import AsyncStorage from '@react-native-async-storage/async-storage'
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

const prefix = 'aerolab:'
/** Read-model cache and pairings stored through the Expo Go compatible adapter. */
export const cacheStore: KeyValueStore = {
  get: k => AsyncStorage.getItem(prefix + k),
  set: (k, v) => AsyncStorage.setItem(prefix + k, v),
  remove: k => AsyncStorage.removeItem(prefix + k),
  async keys() { return (await AsyncStorage.getAllKeys()).filter(k => k.startsWith(prefix)).map(k => k.slice(prefix.length)) },
}

export const random = (n: number): Uint8Array => Crypto.getRandomBytes(n)
