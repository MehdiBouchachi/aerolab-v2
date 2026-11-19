import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { translate, type Language } from '../../../web/src/app/messages'
import { cacheStore } from '../native'

const Context = createContext({ language: 'en' as Language, toggle: () => {} })
export const useLanguage = () => useContext(Context)
export const useTranslation = () => {
  const { language } = useLanguage()
  return (text: string) => translate(language, text)
}

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState<Language>('en')
  useEffect(() => {
    void cacheStore.get('language').then(value => { if (value === 'ar') setLanguage('ar') }).catch(() => {})
  }, [])
  const toggle = () => {
    const next = language === 'en' ? 'ar' : 'en'
    setLanguage(next)
    void cacheStore.set('language', next).catch(() => {})
  }
  return <Context.Provider value={{ language, toggle }}>{children}</Context.Provider>
}
