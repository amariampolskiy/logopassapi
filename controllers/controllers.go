package controllers

import (
	"encoding/json"
	"fmt"
	"log"
	"logopassapi/auth"
	"logopassapi/models"
	"logopassapi/utils"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/tkanos/gonfig"
)

//Controllers struct
type Controllers struct {
	Db     models.Datastore
	Crypto auth.CryptoData
	SMTP   utils.SMTPData
}

//Init func
func Init(dbconfig string, cryptoconfig string, smtpconfig string) Controllers {

	connectionData := models.ConnectionData{}
	if gonfig.GetConf("config/db.json", &connectionData) != nil {
		log.Panic("load db confg error")
	}

	cryptoData := auth.CryptoData{}
	if gonfig.GetConf("config/crypto.json", &cryptoData) != nil {
		log.Panic("load crypto confg error")
	}

	smtpData := utils.SMTPData{}
	if gonfig.GetConf("config/smtp.json", &smtpData) != nil {
		log.Panic("load smtp confg error")
	}

	db, err := models.InitDB(connectionData.ToString())
	if err != nil {
		log.Panic(err)
	}

	return Controllers{Db: db, Crypto: cryptoData, SMTP: smtpData}
}

//GetRouter func
func (c *Controllers) GetRouter() *mux.Router {

	router := mux.NewRouter()

	//login method - tested
	router.HandleFunc("/getauthtoken/", c.GetAuthTokenHandler).Methods("POST")
	//registration method -tested
	router.HandleFunc("/registration/", c.RegistrationHandler).Methods("POST")
	//get an data with token example -tested
	router.HandleFunc("/gettestdatabytoken/", c.GetTestDataByTokenHandler).Methods("POST", "OPTIONS")
	//forgot password method (sending special restore link) -tested
	router.HandleFunc("/getpasswordrestoreemail/", c.SendRestorePasswordEmailHandler).Methods("POST")
	//change password method -tested
	router.HandleFunc("/changepassword/{token}", c.ChangePasswordHandler).Methods("GET")

	return router
}

//ChangePasswordHandler func
func (c *Controllers) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "GET" {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong method!",
			""))
		return
	}

	vars := mux.Vars(r)
	linkData := vars["token"]

	if len(linkData) == 0 {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Empty token!",
			""))
		return
	}

	tokenJSON, err := c.Crypto.DecryptTextAES256(linkData)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Token decryption error!",
			""))
		return
	}

	var token auth.Token
	err = json.Unmarshal([]byte(tokenJSON), &token)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Некорректная ссылка!",
			""))
		return
	}

	if token.TTL-time.Now().Unix() > 0 {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Просроченная ссылка!",
			""))
		return
	}

	userData, _, err := c.Db.GetUserByEmail(token.Email)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Некорректный email!",
			""))
		return
	}

	password, err := auth.GetNewPassword()
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Password generation error!",
			""))
		return
	}

	userData.PswdHashB = c.Crypto.GetSHA256Bytes(password)

	userID, _, err := c.Db.SaveUser(userData)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Save new password error!",
			""))
		return
	}

	if userID > 0 {

		err = c.SMTP.SendEmail(userData.Email, `Subject: Ваш пароль\n `+password)
		if err != nil {
			fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
				false,
				err.Error(),
				""))
			return
		}

		token, _ := c.Crypto.EncryptTextAES256Base64(c.Crypto.GetTokenJSON(userID))

		fmt.Fprintf(w, "%s", utils.GetJSONAnswer(token,
			true,
			"",
			""))

	} else {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Ошибка смены пароля!",
			""))
	}

}

//SendRestorePasswordEmailHandler func
func (c *Controllers) SendRestorePasswordEmailHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong method!",
			""))
		return
	}

	var tokenItem auth.Token
	err := utils.ConvertBody2JSON(r.Body, &tokenItem)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong data!",
			""))
		return
	}

	//check email format
	if !utils.CheckEmailFormat(tokenItem.Email) {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Некорректный EMail формат!",
			""))
		return
	}

	linkData, err := c.Crypto.EncryptTextAES256Base64(fmt.Sprintf(`{"email":"%s", "ttl":%d}`, tokenItem.Email, c.Crypto.PasswordEmailTTL))
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Decrypt error!",
			""))
		return
	}

	err = c.SMTP.SendEmail(tokenItem.Email, `Subject: Смена пароля: `+c.Crypto.RestorePasswordURL+linkData)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"EMail link error!",
			""))
		return
	}

	fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
		true,
		"Вам отправлен EMail с инструкциями!",
		""))
}

//RegistrationHandler func
func (c *Controllers) RegistrationHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong method!",
			""))
		return
	}

	userData := new(models.UserData)
	err := utils.ConvertBody2JSON(r.Body, &userData)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Bad data",
			""))
		return
	}

	userData.Email = strings.ToLower(userData.Email)

	//check email format
	if !utils.CheckEmailFormat(userData.Email) {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Некорректный EMail формат!",
			""))
		return
	}

	//generate password and send it to email
	password, err := auth.GetNewPassword()
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Password error",
			""))
		return
	}

	userData.PswdHashB = c.Crypto.GetSHA256Bytes(password)

	userID, errorCode, err := c.Db.SaveUser(userData)
	if err != nil && errorCode != "22024" {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Save error!",
			""))
		return
	}

	if userID > 0 {

		err = c.SMTP.SendEmail(userData.Email, `Subject: Ваш пароль\n`+password)
		if err != nil {
			fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
				false,
				err.Error(),
				""))
			return
		}

		token, _ := c.Crypto.EncryptTextAES256Base64(c.Crypto.GetTokenJSON(userID))

		fmt.Fprintf(w, "%s", utils.GetJSONAnswer(token,
			true,
			"",
			""))
	} else {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Такой EMail уже используется!",
			""))
	}
}

//GetAuthTokenHandler func
func (c *Controllers) GetAuthTokenHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong method!",
			""))
		return
	}

	var afd auth.LogoPassData
	err := utils.ConvertBody2JSON(r.Body, &afd)
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong data!",
			""))
		return
	}

	userData, _, err := c.Db.GetUserByAuth(afd.Login, c.Crypto.GetSHA256Bytes(afd.Password))
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"User not found!",
			""))
		return
	}

	if userData.UserID > 0 {

		token, _ := c.Crypto.EncryptTextAES256Base64(c.Crypto.GetTokenJSON(userData.UserID))

		fmt.Fprintf(w, "%s", utils.GetJSONAnswer(token,
			true,
			"",
			""))

	} else {

		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Не верный логин или пароль!",
			""))
	}

}

//GetTestDataByTokenHandler func
func (c *Controllers) GetTestDataByTokenHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" && r.Method != "OPTIONS" {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Wrong method!",
			""))
		return
	}

	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		return
	}

	checked, err := c.Crypto.CheckAuthToken(r.Header.Get("Authorization"))
	if err != nil {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Token validation error!",
			""))
		return
	}

	if !checked {
		fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
			false,
			"Невалидный токен!",
			""))
		return
	}

	fmt.Fprintf(w, "%s", utils.GetJSONAnswer("",
		true,
		"",
		`{"param":"value"}`))
}
