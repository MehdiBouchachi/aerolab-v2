import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { translate, type Language } from './messages'

const Context = createContext({ language: 'en' as Language, toggle: () => {} })
export const useLanguage = () => useContext(Context)
export const useTranslation = () => {
  const { language } = useLanguage()
  return (text: string) => translate(language, text)
}

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState<Language>(() => {
    try { return localStorage.getItem('aerolab-language') === 'ar' ? 'ar' : 'en' }
    catch { return 'en' }
  })
  useEffect(() => {
    document.documentElement.lang = language === 'ar' ? 'ar-DZ' : 'en'
    document.documentElement.dir = language === 'ar' ? 'rtl' : 'ltr'
    try { localStorage.setItem('aerolab-language', language) } catch { /* storage may be unavailable */ }
  }, [language])
  return <Context.Provider value={{ language, toggle: () => setLanguage(v => v === 'en' ? 'ar' : 'en') }}>{children}</Context.Provider>
}

export function Tr({ children }: { children: string }) {
  return translate(useLanguage().language, children)
}

export function LanguageButton() {
  const { language, toggle } = useLanguage()
  return <button type="button" className="language-toggle" onClick={toggle} lang={language === 'en' ? 'ar' : 'en'}>
    {language === 'en' ? 'العربية' : 'English'}
  </button>
}
